/*
 * This file is part of the KubeVirt Redfish project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2025 KubeVirt Redfish project and its authors.
 *
 */

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockHandler struct{}

func (m *mockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/redfish/v1/" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Name":"Root Service"}`))
		return
	}
	if r.URL.Path == "/redfish/v1/Chassis" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Members":[]}`))
		return
	}
	if r.URL.Path == "/redfish/v1/Systems" {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Members":[]}`))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"error":"not found"}`))
}

func TestServiceRootHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/redfish/v1/", nil)
	w := httptest.NewRecorder()
	h := &mockHandler{}
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Root Service") {
		t.Errorf("Expected Root Service in response, got %s", w.Body.String())
	}
}

func TestChassisHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/redfish/v1/Chassis", nil)
	w := httptest.NewRecorder()
	h := &mockHandler{}
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Members") {
		t.Errorf("Expected Members in response, got %s", w.Body.String())
	}
}

func TestSystemsHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/redfish/v1/Systems", nil)
	w := httptest.NewRecorder()
	h := &mockHandler{}
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Members") {
		t.Errorf("Expected Members in response, got %s", w.Body.String())
	}
}

func TestNotFoundHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/redfish/v1/Unknown", nil)
	w := httptest.NewRecorder()
	h := &mockHandler{}
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "not found") {
		t.Errorf("Expected not found in response, got %s", w.Body.String())
	}
}
