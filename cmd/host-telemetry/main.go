package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/SentimentalK/host-telemetry/internal/collector"
	"github.com/SentimentalK/host-telemetry/internal/config"
	"github.com/SentimentalK/host-telemetry/internal/events"
	"github.com/SentimentalK/host-telemetry/internal/hostinfo"
	"github.com/SentimentalK/host-telemetry/internal/install"
	"github.com/SentimentalK/host-telemetry/internal/otlp"
	"github.com/SentimentalK/host-telemetry/internal/schema"
	"github.com/SentimentalK/host-telemetry/internal/sender"
	"github.com/SentimentalK/host-telemetry/internal/source"
	"github.com/SentimentalK/host-telemetry/internal/source/darwin"
	"github.com/SentimentalK/host-telemetry/internal/source/fixtures"
	"github.com/SentimentalK/host-telemetry/internal/source/linux"
	"github.com/SentimentalK/host-telemetry/internal/source/windows"
	"github.com/SentimentalK/host-telemetry/internal/wal"
	"github.com/SentimentalK/host-telemetry/internal/workload"
)

const ServiceVersion = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "print":
		if len(os.Args) != 2 {
			usage()
			os.Exit(2)
		}
		printMetrics(os.Stdout)
	case "run":
		run(os.Args[2:])
	case "enroll":
		provision("enroll", os.Args[2:])
	case "install":
		provision("install", os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: host-telemetry print|run|enroll|install [flags]")
}

func run(args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	fixturesDir := flags.String("fixtures", "", "fixture root; empty collects the live host")
	platform := flags.String("platform", runtime.GOOS, "fixture platform")
	listen := flags.String("listen", "127.0.0.1:9101", "listen address")
	endpoint := flags.String("endpoint", "", "OTLP/HTTP endpoint; empty disables sending")
	token := flags.String("token", "", "bearer token")
	sensors := flags.String("sensors", "", "sensor backend; macmon enables darwin sensors")
	walDir := flags.String("wal-dir", defaultWALDir(), "write-ahead log directory")
	configPath := flags.String("config", defaultConfigPath(), "config file path")
	workloadEnabled := flags.Bool("workload", false, "enable workload attribution")
	workloadCgroups := flags.Bool("workload-cgroups", true, "read cgroup v2 scopes")
	workloadExecutables := flags.String("workload-executables", "", "comma-separated process executable whitelist")
	eventsDiskFullRatio := flags.Float64("events-disk-full-ratio", 0, "disk used ratio that emits host.disk.full")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil && !errors.Is(err, config.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "host-telemetry: %v\n", err)
		os.Exit(1)
	}
	cfg = cfg.WithEnv(os.Getenv)
	set := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if !set["listen"] && cfg.Listen != "" {
		*listen = cfg.Listen
	}
	if !set["endpoint"] && cfg.Endpoint != "" {
		*endpoint = cfg.Endpoint
	}
	if !set["token"] && cfg.Token != "" {
		*token = cfg.Token
	}
	if !set["wal-dir"] && cfg.WALDir != "" {
		*walDir = cfg.WALDir
	}
	if !set["sensors"] && cfg.Sensors != "" {
		*sensors = cfg.Sensors
	}
	if !set["workload"] && cfg.Workload.Enabled {
		*workloadEnabled = true
	}
	if !set["workload-cgroups"] && cfg.Workload.Cgroups != nil {
		*workloadCgroups = *cfg.Workload.Cgroups
	}
	if !set["workload-executables"] && cfg.Workload.Executables != nil {
		*workloadExecutables = strings.Join(cfg.Workload.Executables, ",")
	}
	if !set["events-disk-full-ratio"] && cfg.Events.DiskFullRatio != 0 {
		*eventsDiskFullRatio = cfg.Events.DiskFullRatio
	}

	opts := serveOptions{
		Fixtures:            *fixturesDir,
		Platform:            *platform,
		Listen:              *listen,
		Endpoint:            *endpoint,
		Token:               *token,
		Sensors:             *sensors,
		WALDir:              *walDir,
		HostID:              cfg.HostID,
		HostName:            cfg.HostName,
		Workload:            *workloadEnabled,
		WorkloadCgroups:     *workloadCgroups,
		WorkloadExecutables: splitExecutables(*workloadExecutables),
		EventsDiskFullRatio: *eventsDiskFullRatio,
	}
	if isWindowsService() {
		if err := runWindowsService(opts); err != nil {
			fatal(err)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, opts); err != nil {
		fatal(err)
	}
}

type serveOptions struct {
	Fixtures            string
	Platform            string
	Listen              string
	Endpoint            string
	Token               string
	Sensors             string
	WALDir              string
	HostID              string
	HostName            string
	Workload            bool
	WorkloadCgroups     bool
	WorkloadExecutables []string
	EventsDiskFullRatio float64
}

func splitExecutables(value string) []string {
	list := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			list = append(list, part)
		}
	}
	return list
}

func serve(ctx context.Context, opts serveOptions) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	start := time.Now()
	sources, err := loadSources(opts.Fixtures, opts.Platform, opts.Sensors)
	if err != nil {
		return err
	}
	if opts.Workload {
		cgroupRoot, procRoot := "/sys/fs/cgroup", "/"
		if opts.Fixtures != "" {
			cgroupRoot = filepath.Join(opts.Fixtures, opts.Platform, "workload", "cgroup")
			procRoot = filepath.Join(opts.Fixtures, opts.Platform, "workload")
		}
		sources = append(sources, workload.New(os.DirFS(cgroupRoot), os.DirFS(procRoot), workload.Options{
			Cgroups:     opts.WorkloadCgroups,
			Executables: opts.WorkloadExecutables,
		}))
	}

	var store *wal.Store
	var walStats collector.WALStats
	if opts.Endpoint != "" {
		store, err = wal.Open(opts.WALDir, 256<<20)
		if err != nil {
			return err
		}
		walStats = store
	}

	pool := collector.New(sources)
	pool.Sources = append(pool.Sources, collector.NewSelfSource(os.DirFS("/"), pool.Stats, walStats, start))
	for _, src := range pool.Sources {
		pool.Stats.Record(src.Name(), 0)
	}

	eventsPath := filepath.Join(install.DefaultTarget().StateDir(), "events.ndjson")
	if err := os.MkdirAll(filepath.Dir(eventsPath), 0o700); err != nil {
		log.Printf("events: %v", err)
	}

	root := os.DirFS("/")
	hostID := opts.HostID
	if hostID == "" {
		if runtime.GOOS == "windows" {
			hostID, _ = hostinfo.MachineGUID()
		} else {
			hostID, _ = hostinfo.MachineID(root)
		}
	}
	hostName := opts.HostName
	if hostName == "" {
		hostName, _ = hostinfo.Name()
	}
	osType, osVersion, osDescription := hostinfo.OS(root)
	if opts.Fixtures == "" && runtime.GOOS == "darwin" {
		if version, description, err := hostinfo.DarwinOS(func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		}); err == nil {
			osVersion, osDescription = version, description
		}
	}
	resource := otlp.Resource{
		HostID:         hostID,
		HostName:       hostName,
		HostArch:       hostinfo.Arch(),
		OSType:         osType,
		OSVersion:      osVersion,
		OSDescription:  osDescription,
		ServiceName:    "host-telemetry",
		ServiceVersion: ServiceVersion,
		SchemaVersion:  schema.Version,
	}

	sinks := []events.Sink{&events.NDJSON{Path: eventsPath}}
	if store != nil {
		sinks = append(sinks, &events.OTLPSink{Store: store, Resource: resource})
	}
	pipeline := &events.Pipeline{Sinks: sinks, Logf: log.Printf}
	pool.Jump = func(source string, skew time.Duration) {
		pipeline.Emit(events.Event{
			Time:    time.Now(),
			Name:    events.ClockSkew,
			Attrs:   []events.Attr{{Key: "jump_ms", Value: strconv.FormatInt(skew.Milliseconds(), 10)}},
			Subject: "clock",
		})
		log.Printf("host.clock.skew source=%s jump=%s", source, skew)
	}

	snapshot := newSnapshot()
	sink := func(source string, points []schema.Point, err error, d time.Duration) {
		if err != nil {
			log.Printf("source %s: %v", source, err)
		}
		snapshot.update(source, points)
		if store == nil || len(points) == 0 {
			return
		}
		now := time.Now()
		payload, encodeErr := otlp.Encode(points, now, start, resource)
		if encodeErr != nil {
			log.Printf("encode %s: %v", source, encodeErr)
			return
		}
		if _, appendErr := store.Append(now, len(points), payload); appendErr != nil {
			log.Printf("wal %s: %v", source, appendErr)
		}
	}

	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		pool.Run(ctx, sink)
	}()
	if store != nil {
		client := &otlp.Client{Config: otlp.Config{Endpoint: opts.Endpoint, Token: opts.Token}}
		transport := &sender.Sender{Store: store, Client: client}
		workers.Add(1)
		go func() {
			defer workers.Done()
			transport.Run(ctx)
		}()
	}
	if runtime.GOOS == "linux" {
		detectors := events.LinuxDetectors(events.DetectorOptions{FS: os.DirFS("/"), DiskFullRatio: opts.EventsDiskFullRatio})
		workers.Add(1)
		go func() {
			defer workers.Done()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					for _, detector := range detectors {
						for _, ev := range detector.Poll(now) {
							pipeline.Emit(ev)
						}
					}
				}
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", collector.MetricsHandler(snapshot.list))
	server := &http.Server{Addr: opts.Listen, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", opts.Listen)
	serveErr := server.ListenAndServe()
	cancel()
	workers.Wait()
	if err := pipeline.Close(); err != nil {
		log.Printf("events close: %v", err)
	}
	if store != nil {
		if err := store.Close(); err != nil {
			log.Printf("wal close: %v", err)
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}

func loadSources(fixtureRoot, platform, sensors string) ([]source.Source, error) {
	if fixtureRoot != "" {
		sources, err := fixtures.LoadPlatform(fixtureRoot, platform)
		if err != nil {
			return nil, err
		}
		if platform == "darwin" && sensors != "macmon" {
			filtered := make([]source.Source, 0, len(sources))
			for _, src := range sources {
				if src.Name() != "darwin-sensors" {
					filtered = append(filtered, src)
				}
			}
			sources = filtered
		}
		return sources, nil
	}
	if platform != runtime.GOOS {
		return nil, fmt.Errorf("live collection requires platform %s, running on %s", platform, runtime.GOOS)
	}
	switch platform {
	case "linux":
		return linux.NewLive(os.DirFS("/"), linux.Options{}), nil
	case "darwin":
		return darwin.NewLive(darwin.Options{Sensors: sensors == "macmon"}), nil
	case "windows":
		return windows.NewLive(windows.Options{}), nil
	default:
		return nil, fmt.Errorf("live collection supports linux, darwin and windows only, running on %s", platform)
	}
}

func defaultWALDir() string {
	return filepath.Join(install.DefaultTarget().StateDir(), "wal")
}

func defaultConfigPath() string {
	return install.DefaultTarget().ConfigPath()
}

func provision(name string, args []string) {
	flags := flag.NewFlagSet(name, flag.ExitOnError)
	endpoint := flags.String("endpoint", "", "OTLP/HTTP endpoint")
	token := flags.String("token", "", "bearer token")
	user := flags.Bool("user", false, "install a user-scoped service")
	dryRun := flags.Bool("dry-run", false, "print actions without applying them")
	platform := flags.String("platform", runtime.GOOS, "target platform")
	hostIDOverride := flags.String("host-id", "", "override the resolved host id")
	sensors := flags.String("sensors", "", "sensor backend; macmon enables darwin sensors")
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}

	hostID, err := resolveHostID(*platform, *hostIDOverride)
	if err != nil {
		fatal(err)
	}
	hostName, err := hostinfo.Name()
	if err != nil {
		fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	if name == "install" && strings.Contains(executable, "/go-build") {
		fmt.Fprintln(os.Stderr, "warning: ExecStart points at a go run temp binary; run install from an installed binary")
	}
	cfg := config.Config{
		Endpoint: *endpoint,
		Token:    *token,
		HostID:   hostID,
		HostName: hostName,
		Sensors:  *sensors,
	}
	actions, err := install.Plan(install.Target{Platform: *platform, User: *user, Executable: executable}, cfg)
	if err != nil {
		fatal(err)
	}
	if name == "enroll" {
		actions = actions[:1]
	}
	if *dryRun {
		install.Render(os.Stdout, actions)
		fmt.Printf("host.id=%s\n", hostID)
		return
	}
	if err := install.Apply(actions); err != nil {
		fatal(err)
	}
	fmt.Printf("host.id=%s\n", hostID)
}

func resolveHostID(platform, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	switch platform {
	case "linux":
		return hostinfo.MachineID(os.DirFS("/"))
	case "darwin":
		return hostinfo.IOPlatformUUID(func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).Output()
		})
	case "windows":
		return hostinfo.MachineGUID()
	default:
		return "", fmt.Errorf("host.id is not available on platform %s", platform)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "host-telemetry: %v\n", err)
	os.Exit(1)
}

type snapshot struct {
	mu      sync.Mutex
	sources map[string]map[string]schema.Point
}

func newSnapshot() *snapshot {
	return &snapshot{sources: make(map[string]map[string]schema.Point)}
}

func (s *snapshot) update(source string, points []schema.Point) {
	s.mu.Lock()
	defer s.mu.Unlock()
	series := make(map[string]schema.Point, len(points))
	for _, point := range points {
		series[seriesKey(point)] = point
	}
	s.sources[source] = series
}

func (s *snapshot) list() []schema.Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []schema.Point
	for _, series := range s.sources {
		for _, point := range series {
			out = append(out, point)
		}
	}
	sort.Slice(out, func(i, j int) bool { return seriesLess(out[i], out[j]) })
	return out
}

func seriesKey(point schema.Point) string {
	var b strings.Builder
	b.WriteString(point.Name)
	for _, attr := range point.Attrs {
		b.WriteByte(0)
		b.WriteString(attr.Key)
		b.WriteByte('=')
		b.WriteString(attr.Value)
	}
	return b.String()
}

func seriesLess(a, b schema.Point) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	for i := 0; i < len(a.Attrs) && i < len(b.Attrs); i++ {
		if a.Attrs[i].Key != b.Attrs[i].Key {
			return a.Attrs[i].Key < b.Attrs[i].Key
		}
		if a.Attrs[i].Value != b.Attrs[i].Value {
			return a.Attrs[i].Value < b.Attrs[i].Value
		}
	}
	return len(a.Attrs) < len(b.Attrs)
}

func printMetrics(w io.Writer) {
	defs := append([]schema.MetricDef(nil), schema.All()...)
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	for _, def := range defs {
		names := make([]string, 0, len(def.Attrs))
		for _, attr := range def.Attrs {
			names = append(names, attr.Name)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", def.Name, def.Unit, kindName(def.Kind), strings.Join(names, ","))
	}
}

func kindName(kind schema.Kind) string {
	switch kind {
	case schema.KindGauge:
		return "KindGauge"
	case schema.KindCounter:
		return "KindCounter"
	case schema.KindUpDownCounter:
		return "KindUpDownCounter"
	case schema.KindHistogram:
		return "KindHistogram"
	default:
		return "Kind(0)"
	}
}
