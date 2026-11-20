package agent

import (
	"bytes"
	"errors"
	"io"
	"net/http"
)

func OpenLocal(o Options, id string, handler http.Handler) (*Agent, error) {
	if id == "" || handler == nil {
		return nil, errors.New("local probe requires an identity and a Core handler")
	}
	a, err := open(o, id)
	if err != nil {
		return nil, err
	}
	a.client.Transport = localTransport{handler: handler}
	return a, nil
}

type localTransport struct {
	handler http.Handler
}

func (t localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer req.Body.Close()
	}
	w := &localResponse{header: make(http.Header)}
	t.handler.ServeHTTP(w, req)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return &http.Response{
		StatusCode: w.status, Header: w.header, Body: io.NopCloser(&w.body),
		ContentLength: int64(w.body.Len()), Request: req,
	}, nil
}

type localResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *localResponse) Header() http.Header { return w.header }

func (w *localResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *localResponse) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(data)
}
