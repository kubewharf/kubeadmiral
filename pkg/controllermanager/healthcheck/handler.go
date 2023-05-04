package healthcheck

import (
	"net/http"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

type MutableHealthCheckHandler struct {
	handler http.Handler

	mu            sync.RWMutex
	readyzHandler *healthz.Handler
	livezHandler  *healthz.Handler
}

func NewMutableHealthCheckHandler() *MutableHealthCheckHandler {
	h := &MutableHealthCheckHandler{
		mu:            sync.RWMutex{},
		readyzHandler: &healthz.Handler{Checks: map[string]healthz.Checker{}},
		livezHandler:  &healthz.Handler{Checks: map[string]healthz.Checker{}},
	}

	mux := http.NewServeMux()
	mux.Handle("/readyz", http.StripPrefix("/readyz", h.readyzHandler))
	mux.Handle("/readyz/", http.StripPrefix("/readyz/", h.readyzHandler))
	mux.Handle("/livez", http.StripPrefix("/livez", h.livezHandler))
	mux.Handle("/livez/", http.StripPrefix("/livez/", h.livezHandler))

	h.handler = mux

	return h
}

func (h *MutableHealthCheckHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	h.handler.ServeHTTP(writer, request)
}

func (h *MutableHealthCheckHandler) AddReadyzChecker(name string, checker healthz.Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.readyzHandler.Checks[name] = checker
}

func (h *MutableHealthCheckHandler) AddLivezChecker(name string, checker healthz.Checker) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.livezHandler.Checks[name] = checker
}

var _ http.Handler = &MutableHealthCheckHandler{}
