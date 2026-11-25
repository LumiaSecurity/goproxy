package goproxy_test

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two pipelined requests arrive in one TLS write, so the second sits in the
// parser's read-ahead while the first is served; each capture must hold only
// its own request's bytes.
func TestWireTapSplitsPipelinedRequests(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, "echo:"+r.URL.Path+":"+string(body))
	}))
	defer upstream.Close()

	type capture struct {
		stage goproxy.WireStage
		raw   string
	}
	var (
		mu       sync.Mutex
		captures []capture
	)
	proxy := goproxy.NewProxyHttpServer()
	proxy.Tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.WireTap = func(_ *goproxy.ProxyCtx, stage goproxy.WireStage, raw []byte) {
		mu.Lock()
		defer mu.Unlock()
		captures = append(captures, capture{stage, string(raw)})
	}
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	host := strings.TrimPrefix(upstream.URL, "https://")
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxyServer.URL, "http://"))
	require.NoError(t, err)
	defer conn.Close()
	_, err = io.WriteString(conn, "CONNECT "+host+" HTTP/1.1\r\nHost: "+host+"\r\n\r\n")
	require.NoError(t, err)
	connectResp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, connectResp.StatusCode)

	tlsConn := tls.Client(conn, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}})
	defer tlsConn.Close()
	req1 := "POST /one HTTP/1.1\r\nHost: " + host + "\r\nContent-Length: 3\r\n\r\nabc"
	req2 := "GET /two HTTP/1.1\r\nHost: " + host + "\r\n\r\n"
	_, err = io.WriteString(tlsConn, req1+req2)
	require.NoError(t, err)

	br := bufio.NewReader(tlsConn)
	for _, want := range []string{"echo:/one:abc", "echo:/two:"} {
		resp, err := http.ReadResponse(br, nil)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, want, string(body))
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, captures, 4)
	assert.Equal(t, capture{goproxy.WireClientRequestIn, req1}, captures[0])
	assert.Equal(t, goproxy.WireClientResponseOut, captures[1].stage)
	assert.Contains(t, captures[1].raw, "echo:/one:abc")
	assert.Equal(t, capture{goproxy.WireClientRequestIn, req2}, captures[2])
	assert.Equal(t, goproxy.WireClientResponseOut, captures[3].stage)
	assert.Contains(t, captures[3].raw, "echo:/two:")
	assert.NotContains(t, captures[3].raw, "echo:/one", "each response capture holds only its own bytes")
}
