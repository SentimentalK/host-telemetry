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
	"github.com/SentimentalK/host-telemetry/internal/wal"
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

	start := time.Now()
	sources, err := loadSources(*fixturesDir, *platform, *sensors)
	if err != nil {
		fmt.Fprintf(os.Stderr, "host-telemetry: %v\n", err)
		os.Exit(1)
	}

	var store *wal.Store
	var walStats collector.WALStats
	if *endpoint != "" {
		store, err = wal.Open(*walDir, 256<<20)
		if err != nil {
			fmt.Fprintf(os.Stderr, "host-telemetry: %v\n", err)
			os.Exit(1)
		}
		walStats = store
	}

	pool := collector.New(sources)
	pool.Sources = append(pool.Sources, collector.NewSelfSource(os.DirFS("/"), pool.Stats, walStats, start))
	for _, src := range pool.Sources {
		pool.Stats.Record(src.Name(), 0)
	}

	eventsPath := filepath.Join(install.Target{User: os.Geteuid() != 0}.StateDir(), "events.ndjson")
	if err := os.MkdirAll(filepath.Dir(eventsPath), 0o700); err != nil {
		log.Printf("events: %v", err)
	}
	eventLog := &events.Writer{Path: eventsPath}
	pool.Jump = func(source string, skew time.Duration) {
		if err := eventLog.Append(events.Event{Time: time.Now(), Name: events.ClockSkew, JumpMS: skew.Milliseconds()}); err != nil {
			log.Printf("events: %v", err)
		}
		log.Printf("host.clock.skew source=%s jump=%s", source, skew)
	}

	root := os.DirFS("/")
	hostID := cfg.HostID
	if hostID == "" {
		hostID, _ = hostinfo.MachineID(root)
	}
	hostName := cfg.HostName
	if hostName == "" {
		hostName, _ = hostinfo.Name()
	}
	osType, osVersion, osDescription := hostinfo.OS(root)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		pool.Run(ctx, sink)
	}()
	if store != nil {
		client := &otlp.Client{Config: otlp.Config{Endpoint: *endpoint, Token: *token}}
		transport := &sender.Sender{Store: store, Client: client}
		workers.Add(1)
		go func() {
			defer workers.Done()
			transport.Run(ctx)
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", collector.MetricsHandler(snapshot.list))
	server := &http.Server{Addr: *listen, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", *listen)
	serveErr := server.ListenAndServe()
	stop()
	workers.Wait()
	if store != nil {
		if err := store.Close(); err != nil {
			log.Printf("wal close: %v", err)
		}
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "host-telemetry: %v\n", serveErr)
		os.Exit(1)
	}
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
	default:
		return nil, fmt.Errorf("live collection supports linux and darwin only, running on %s", platform)
	}
}

func defaultWALDir() string {
	return filepath.Join(install.Target{User: os.Geteuid() != 0}.StateDir(), "wal")
}

func defaultConfigPath() string {
	return install.Target{User: os.Geteuid() != 0}.ConfigPath()
}

func provision(name string, args []string) {
	flags := flag.NewFlagSet(name, flag.ExitOnError)
	endpoint := flags.String("endpoint", "", "OTLP/HTTP endpoint")
	token := flags.String("token", "", "bearer token")
	user := flags.Bool("user", false, "install a systemd user unit")
	dryRun := flags.Bool("dry-run", false, "print actions without applying them")
	platform := flags.String("platform", runtime.GOOS, "target platform")
	hostIDOverride := flags.String("host-id", "", "override the resolved host id")
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
