package collector

import (
	"net/http"

	"github.com/SentimentalK/host-telemetry/internal/schema"
)

func MetricsHandler(snapshot func() []schema.Point) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		if err := WriteMetrics(w, snapshot()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
