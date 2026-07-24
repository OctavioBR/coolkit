// Small HTTP service with a separate observability listener.
//
// The API listener (HTTP_PORT, default 8080) serves the application:
//   - GET  /estimate/coolkit -> the pod/host name and current estimate serving the request
//   - POST /estimate/coolkit -> set the estimate, a non-negative integer (also accepts PUT with ?value= query parameter)
//
// The observability listener (METRICS_PORT, default 2345) serves:
//   - GET  /healthz -> liveness/readiness probe
//   - GET  /metrics -> Prometheus text-format metrics
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultHTTPPort    = "8080"
	defaultMetricsPort = "2345"
	shutdownTimeout    = 10 * time.Second
)

// estimate holds the current service estimate, a non-negative integer.
var estimate atomic.Int64

// requests counts HTTP requests served on the API listener, exposed as a Prometheus counter.
var requests atomic.Int64

// Response is the JSON body returned by the /estimate/coolkit endpoint.
type Response struct {
	Estimate int64  `json:"estimate"`
	Hostname string `json:"hostname"`
}

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)

	apiServer := &http.Server{
		Addr:    ":" + getenv("HTTP_PORT", defaultHTTPPort),
		Handler: apiMux(),
	}
	metricsServer := &http.Server{
		Addr:    ":" + getenv("METRICS_PORT", defaultMetricsPort),
		Handler: metricsMux(),
	}

	// Either a fatal listen error or a termination signal ends the wait below.
	errCh := make(chan error, 2)
	go serve(apiServer, "api", errCh)
	go serve(metricsServer, "observability", errCh)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("received signal %s, shutting down", sig)
	case err := <-errCh:
		log.Printf("fatal server error: %v, shutting down", err)
	}

	// Drain in-flight requests on both listeners before exiting.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdown(ctx, apiServer, "api")
	shutdown(ctx, metricsServer, "observability")
}

func serve(s *http.Server, name string, errCh chan<- error) {
	log.Printf("%s listening on %s", name, s.Addr)
	if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- fmt.Errorf("%s listener: %w", name, err)
	}
}

func shutdown(ctx context.Context, s *http.Server, name string) {
	if err := s.Shutdown(ctx); err != nil {
		log.Printf("%s listener shutdown error: %v", name, err)
	}
}

func apiMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/estimate/coolkit", handleEstimate)
	return countRequests(mux)
}

func metricsMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealthz)
	mux.HandleFunc("/metrics", handleMetrics)
	return mux
}

// countRequests increments the request counter for every API request.
func countRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		next.ServeHTTP(w, r)
	})
}

func handleEstimate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		name, err := os.Hostname()
		if err != nil {
			http.Error(w, "could not determine hostname", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(Response{Estimate: estimate.Load(), Hostname: name})
	case http.MethodPost, http.MethodPut:
		raw, err := estimateValueFromRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			http.Error(w, fmt.Sprintf("estimate must be an integer, got %q", raw), http.StatusBadRequest)
			return
		}
		if n < 0 {
			http.Error(w, "estimate must be non-negative", http.StatusBadRequest)
			return
		}
		estimate.Store(int64(n))
		name, err := os.Hostname()
		if err != nil {
			http.Error(w, "could not determine hostname", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(Response{Estimate: int64(n), Hostname: name})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// estimateValueFromRequest reads the desired estimate from the "value" query
// parameter if present, otherwise from the request body.
func estimateValueFromRequest(r *http.Request) (string, error) {
	if v := r.URL.Query().Get("value"); v != "" {
		return strings.TrimSpace(v), nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<10))
	if err != nil {
		return "", fmt.Errorf("could not read request body: %v", err)
	}
	v := strings.TrimSpace(string(body))
	if v == "" {
		return "", errors.New("provide estimate via request body or the ?value= query parameter")
	}
	return v, nil
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprint(w,
		"# HELP coolkit_up Whether the service is up (always 1 when scraped).\n"+
			"# TYPE coolkit_up gauge\n"+
			"coolkit_up 1\n")
	fmt.Fprintf(w,
		"# HELP coolkit_service_estimate Current service estimate, a non-negative integer.\n"+
			"# TYPE coolkit_service_estimate gauge\n"+
			"coolkit_service_estimate %d\n", estimate.Load())
	fmt.Fprintf(w,
		"# HELP coolkit_http_requests_total Total requests served on the API listener.\n"+
			"# TYPE coolkit_http_requests_total counter\n"+
			"coolkit_http_requests_total %d\n", requests.Load())
}

// getenv returns the value of the environment variable key, or fallback when
// it is unset or empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
