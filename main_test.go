package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestGetSubscription(t *testing.T) {
	lines := []string{
		"ss://" + base64.URLEncoding.EncodeToString([]byte("aes-256-gcm:password@example.com:8388")) + "#legacy",
		"vmess://" + base64.URLEncoding.EncodeToString([]byte(`{"ps":"tag","port":"8388","id":"uuid","aid":0,"net":"tcp","type":"none","tls":"none","add":"example.com"}`)),
	}

	body := base64.URLEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	got, err := getSubscription(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ss://aes-256-gcm:password@example.com:8388",
		"vmess://auto:uuid@example.com:8388",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
