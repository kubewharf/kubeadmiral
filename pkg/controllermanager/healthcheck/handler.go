/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

This file may have been modified by The KubeAdmiral Authors
("KubeAdmiral Modifications"). All KubeAdmiral Modifications
are Copyright 2023 The KubeAdmiral Authors.
*/

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

func (h *MutableHealthCheckHandler) RemoveReadyzChecker(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.readyzHandler.Checks, name)
}

func (h *MutableHealthCheckHandler) RemoveLivezChecker(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.livezHandler.Checks, name)
}

var _ http.Handler = &MutableHealthCheckHandler{}
