package core

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
)

func captureUpstreamRequest(t *testing.T, backend *Backend, build func(r *http.Request)) string {
	t.Helper()
	var got string
	var up *httptest.Server
	up = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var keys []string
		for k := range r.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %s host=HOST cl=%d te=%v body=%q\n", r.Method, r.URL.RequestURI(), r.ContentLength, r.TransferEncoding, body)
		for _, k := range keys {
			fmt.Fprintf(&sb, "%s: %s\n", k, strings.Join(r.Header[k], ","))
		}
		if r.Host != up.Listener.Addr().String() {
			fmt.Fprintf(&sb, "BAD HOST %s\n", r.Host)
		}
		got = sb.String()
		w.WriteHeader(http.StatusNotFound)
	}))
	defer up.Close()
	target, _ := url.Parse(up.URL)

	c := &transcriptionCall{proxyBody: []byte("REWRITTEN"), proxyContentType: "multipart/form-data; boundary=NEW"}
	in := httptest.NewRequest(http.MethodPost, "http://gw.example/v1/audio/transcriptions?intent=edit&x=1", strings.NewReader("ORIGINAL-BODY"))
	in.RemoteAddr = "203.0.113.9:4444"
	in.Header.Set("Content-Type", "multipart/form-data; boundary=OLD")
	in.Header.Set("Authorization", "Bearer client-token")
	in.Header.Set("User-Agent", "DictionTest/1")
	in.Header.Set("Connection", "keep-alive, X-Hop")
	in.Header.Set("X-Hop", "drop-me")
	in.Header.Set("Te", "trailers")
	in.Header.Set("X-Custom", "a")
	in.Header.Add("X-Custom", "b")
	build(in)
	rec := httptest.NewRecorder()
	c.buildProxy(target, backend).ServeHTTP(rec, in)
	return got
}

func TestBuildProxy_UpstreamRequestExact(t *testing.T) {
	const reqLine = "POST %s host=HOST cl=9 te=[] body=\"REWRITTEN\"\n"
	const common = "Accept-Encoding: gzip\nContent-Length: 9\nContent-Type: multipart/form-data; boundary=NEW\nTe: trailers\nUser-Agent: DictionTest/1\nX-Custom: a,b\n"
	const path = "/v1/audio/transcriptions?intent=edit&x=1"
	cases := []struct {
		name    string
		backend *Backend
		prep    func(r *http.Request)
		want    string
	}{
		{"plain", &Backend{Name: "b"}, func(*http.Request) {},
			fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"target_path_and_auth", &Backend{Name: "b", TargetPath: "/v1/custom", AuthHeader: "Bearer upstream"}, func(*http.Request) {},
			fmt.Sprintf(reqLine, "/v1/custom?intent=edit&x=1") + "Accept-Encoding: gzip\nAuthorization: Bearer upstream\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"inbound_xff", &Backend{Name: "b"}, func(r *http.Request) { r.Header.Set("X-Forwarded-For", "198.51.100.7") },
			fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 198.51.100.7, 203.0.113.9\n"},
		{"two_xff_lines", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Add("X-Forwarded-For", "198.51.100.7")
			r.Header.Add("X-Forwarded-For", "10.0.0.1")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 198.51.100.7, 10.0.0.1, 203.0.113.9\n"},
		{"inbound_forwarded_headers", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
			r.Header.Set("X-Forwarded-Host", "evil.example")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-Port", "8443")
			r.Header.Set("Forwarded", "for=198.51.100.7;proto=https")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\nContent-Length: 9\nContent-Type: multipart/form-data; boundary=NEW\nForwarded: for=198.51.100.7;proto=https\nTe: trailers\nUser-Agent: DictionTest/1\nX-Custom: a,b\nX-Forwarded-For: 198.51.100.7, 10.0.0.1, 203.0.113.9\nX-Forwarded-Host: evil.example\nX-Forwarded-Port: 8443\nX-Forwarded-Proto: https\n"},
		{"connection_names_xff", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "198.51.100.7")
			r.Header.Set("Connection", "keep-alive, X-Hop, X-Forwarded-For")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"connection_names_xfh_lowercase", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Set("X-Forwarded-Host", "evil.example")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("Connection", "keep-alive, x-forwarded-host")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\nX-Forwarded-Proto: https\nX-Hop: drop-me\n"},
		{"two_connection_lines", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "198.51.100.7")
			r.Header.Set("Forwarded", "for=198.51.100.7")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header["Connection"] = []string{"keep-alive, X-Hop", "forwarded", "X-FORWARDED-FOR"}
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\nX-Forwarded-Proto: https\n"},
		{"connection_names_content_type", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Set("Connection", "keep-alive, X-Hop, content-type")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\nContent-Length: 9\nTe: trailers\nUser-Agent: DictionTest/1\nX-Custom: a,b\nX-Forwarded-For: 203.0.113.9\n"},
		{"connection_names_authorization", &Backend{Name: "b", AuthHeader: "Bearer upstream"}, func(r *http.Request) {
			r.Header.Set("Connection", "keep-alive, X-Hop, Authorization")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"connection_unicode_space_authorization", &Backend{Name: "b", AuthHeader: "Bearer upstream"}, func(r *http.Request) {
			r.Header.Set("Connection", "keep-alive, X-Hop, \u00a0Authorization")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer upstream\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"connection_unicode_space_content_type", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Set("Connection", "keep-alive, X-Hop, Content-Type\u00a0")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"connection_unicode_space_xff", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header.Set("X-Forwarded-For", "198.51.100.7")
			r.Header.Set("Connection", "keep-alive, X-Hop, X-Forwarded-For\u0085")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 198.51.100.7, 203.0.113.9\n"},
		{"empty_xff_slice_populated", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header["X-Forwarded-For"] = []string{}
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"query_unparsable_params_kept", &Backend{Name: "b"}, func(r *http.Request) {
			r.URL.RawQuery = "a=1;b=2&c=%zz&d=3"
		}, fmt.Sprintf(reqLine, "/v1/audio/transcriptions?a=1;b=2&c=%zz&d=3") + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 203.0.113.9\n"},
		{"nil_xff_not_populated", &Backend{Name: "b"}, func(r *http.Request) {
			r.Header["X-Forwarded-For"] = nil
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):]},
		{"bad_remote_addr_no_append", &Backend{Name: "b"}, func(r *http.Request) {
			r.RemoteAddr = "not-an-addr"
			r.Header.Set("X-Forwarded-For", "198.51.100.7")
		}, fmt.Sprintf(reqLine, path) + "Accept-Encoding: gzip\nAuthorization: Bearer client-token\n" + common[len("Accept-Encoding: gzip\n"):] + "X-Forwarded-For: 198.51.100.7\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := captureUpstreamRequest(t, tc.backend, tc.prep)
			if got != tc.want {
				t.Fatalf("upstream request changed\n got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}
