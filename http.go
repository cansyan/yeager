package main

// This HTTP proxy is adapted from the httpproxy library in outline-sdk,
// which is more intuitive and clear than mine (written with TCP server).
// See details in https://github.com/Jigsaw-Code/outline-sdk/tree/main/x/httpproxy

import (
	"bufio"
	"io"
	"log"
	"net/http"

	"golang.org/x/net/proxy"
)

type proxyHandler struct {
	dialer proxy.ContextDialer
}

// NewProxyHandler creates a http.Handler that acts as a web proxy
// to reach the destination using the given dialer.
func NewProxyHandler(dialer proxy.ContextDialer) *proxyHandler {
	return &proxyHandler{dialer: dialer}
}

func (h *proxyHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if req.Method == http.MethodConnect {
		h.serveHTTPConnect(resp, req)
		return
	}
	h.serveHTTPForward(resp, req)
}

func (h *proxyHandler) serveHTTPConnect(resp http.ResponseWriter, req *http.Request) {
	if req.Host == "" {
		http.Error(resp, "missing host", http.StatusBadRequest)
		return
	}
	if req.URL.Port() == "" {
		http.Error(resp, "missing port in address", http.StatusBadRequest)
		return
	}
	proxyConn, err := h.dialer.DialContext(req.Context(), "tcp", req.Host)
	if err != nil {
		http.Error(resp, "Failed to connect target", http.StatusServiceUnavailable)
		log.Printf("connect %s: %s", req.Host, err)
		return
	}
	defer proxyConn.Close()

	hijacker, ok := resp.(http.Hijacker)
	if !ok {
		http.Error(resp, "Failed to hijack", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(resp, "Failed to hijack connection", http.StatusInternalServerError)
		log.Print(err)
		return
	}
	defer conn.Close()

	// inform the client
	conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))

	if err = relay(conn, proxyConn); err != nil {
		debugf("relay: %s", err)
	}
}

func (h *proxyHandler) serveHTTPForward(resp http.ResponseWriter, req *http.Request) {
	if req.Host == "" {
		http.Error(resp, "missing host", http.StatusBadRequest)
		return
	}
	host := req.Host
	if req.URL.Port() == "" {
		host += ":80"
	}
	proxyConn, err := h.dialer.DialContext(req.Context(), "tcp", host)
	if err != nil {
		http.Error(resp, "Failed to connect target", http.StatusServiceUnavailable)
		log.Print(err)
		return
	}
	defer proxyConn.Close()

	err = req.Write(proxyConn)
	if err != nil {
		http.Error(resp, "Failed to send request", http.StatusServiceUnavailable)
		log.Print(err)
		return
	}
	proxyResp, err := http.ReadResponse(bufio.NewReader(proxyConn), req)
	if err != nil {
		http.Error(resp, "Failed to read target response", http.StatusServiceUnavailable)
		log.Printf("read target response: %s", err)
		return
	}
	defer proxyResp.Body.Close()

	for key, values := range proxyResp.Header {
		for _, value := range values {
			resp.Header().Add(key, value)
		}
	}
	_, err = io.Copy(resp, proxyResp.Body)
	if err != nil {
		log.Printf("write response: %s", err)
		return
	}
}
