package portforwardhttp

import (
	"io"
	"net/http"
	"time"
)

var Client = &http.Client{
	Transport: &http.Transport{
		DisableKeepAlives: true,
		ForceAttemptHTTP2: false,
	},
	Timeout: 5 * time.Minute,
}

func CloseResp(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}
