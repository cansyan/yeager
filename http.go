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

func (h *proxyHandler) ServeHTTP(proxyResp http.ResponseWriter, proxyReq *http.Request) {
	if proxyReq.Method == http.MethodConnect {
		h.serveHTTPConnect(proxyResp, proxyReq)
		return
	}
	h.serveHTTPForward(proxyResp, proxyReq)
}

func (h *proxyHandler) serveHTTPConnect(proxyResp http.ResponseWriter, proxyReq *http.Request) {
	if proxyReq.Host == "" {
		http.Error(proxyResp, "missing host", http.StatusBadRequest)
		return
	}
	if proxyReq.URL.Port() == "" {
		http.Error(proxyResp, "missing port in address", http.StatusBadRequest)
		return
	}
	stream, err := h.dialer.DialContext(proxyReq.Context(), "tcp", proxyReq.Host)
	if err != nil {
		http.Error(proxyResp, "Failed to connect target", http.StatusServiceUnavailable)
		log.Printf("connect %s: %s", proxyReq.Host, err)
		return
	}
	defer stream.Close()

	hijacker, ok := proxyResp.(http.Hijacker)
	if !ok {
		http.Error(proxyResp, "Failed to hijack", http.StatusInternalServerError)
		return
	}
	proxyConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(proxyResp, "Failed to hijack connection", http.StatusInternalServerError)
		log.Print(err)
		return
	}
	defer proxyConn.Close()

	// inform the client
	proxyConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))

	if err = relay(proxyConn, stream); err != nil {
		debugf("relay: %s", err)
	}
}

func (h *proxyHandler) serveHTTPForward(proxyResp http.ResponseWriter, proxyReq *http.Request) {
	if proxyReq.Host == "" {
		http.Error(proxyResp, "missing host", http.StatusBadRequest)
		return
	}
	host := proxyReq.Host
	if proxyReq.URL.Port() == "" {
		host += ":80"
	}
	targetConn, err := h.dialer.DialContext(proxyReq.Context(), "tcp", host)
	if err != nil {
		http.Error(proxyResp, "Failed to connect target", http.StatusServiceUnavailable)
		log.Print(err)
		return
	}
	defer targetConn.Close()

	err = proxyReq.Write(targetConn)
	if err != nil {
		http.Error(proxyResp, "Failed to send request", http.StatusServiceUnavailable)
		log.Print(err)
		return
	}
	targetResp, err := http.ReadResponse(bufio.NewReader(targetConn), proxyReq)
	if err != nil {
		http.Error(proxyResp, "Failed to read target response", http.StatusServiceUnavailable)
		log.Printf("read target response: %s", err)
		return
	}
	defer targetResp.Body.Close()

	for key, values := range targetResp.Header {
		for _, value := range values {
			proxyResp.Header().Add(key, value)
		}
	}
	_, err = io.Copy(proxyResp, targetResp.Body)
	if err != nil {
		log.Printf("write response: %s", err)
		return
	}
}
