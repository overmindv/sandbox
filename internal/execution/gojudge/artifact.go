package gojudge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

const maxArtifactBytes = 8 << 20

func (c *Client) fileSession() (*Client, func()) {
	tr := c.transport.Clone()
	tr.MaxConnsPerHost = 1
	tr.MaxIdleConnsPerHost = 1
	dial := tr.DialContext
	var opened atomic.Bool
	// File IDs are local to one backend. Never reconnect after the first dial.
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if !opened.CompareAndSwap(false, true) {
			return nil, errors.New("go-judge file session was lost")
		}
		return dial(ctx, network, address)
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   c.httpClient.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Client{baseURL: c.baseURL, httpClient: client, transport: tr}, tr.CloseIdleConnections
}

func (c *Client) getArtifact(ctx context.Context, id string) ([]byte, error) {
	data, err := c.readArtifact(ctx, id, maxArtifactBytes)
	if err != nil {
		return nil, err
	}
	if !isELF(data) {
		return nil, ErrProtocol
	}
	return data, nil
}

func (c *Client) readArtifact(ctx context.Context, id string, maxBytes int64) ([]byte, error) {
	if !validFileID(id) {
		return nil, ErrProtocol
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/file/"+id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.artifactRequest(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > maxBytes {
		return nil, ErrProtocol
	}
	var data []byte
	if resp.ContentLength > 0 {
		data = make([]byte, resp.ContentLength)
		_, err = io.ReadFull(resp.Body, data)
	} else {
		data, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read compiled artifact", ErrInfrastructure)
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrProtocol
	}
	return data, nil
}

func (c *Client) putArtifact(ctx context.Context, data []byte) (string, error) {
	if len(data) > maxArtifactBytes || !isELF(data) {
		return "", ErrProtocol
	}
	var envelope bytes.Buffer
	form := multipart.NewWriter(&envelope)
	if _, err := form.CreateFormFile("file", goArtifactName); err != nil {
		return "", err
	}
	headerLen := envelope.Len()
	if err := form.Close(); err != nil {
		return "", err
	}
	payload := envelope.Bytes()
	body := io.MultiReader(bytes.NewReader(payload[:headerLen]), bytes.NewReader(data), bytes.NewReader(payload[headerLen:]))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/file"), body)
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(payload) + len(data))
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := c.artifactRequest(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	receipt, err := io.ReadAll(io.LimitReader(resp.Body, 129))
	if err != nil {
		return "", fmt.Errorf("%w: read artifact receipt", ErrInfrastructure)
	}
	var id string
	if len(receipt) > 128 || json.Unmarshal(receipt, &id) != nil || !validFileID(id) {
		return "", ErrProtocol
	}
	return id, nil
}

func isELF(data []byte) bool {
	return bytes.HasPrefix(data, []byte("\x7fELF"))
}

func (c *Client) deleteArtifact(ctx context.Context, id string) {
	if !validFileID(id) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint("/file/"+id), nil)
	if err != nil {
		return
	}
	if resp, err := c.httpClient.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
	}
}

func (c *Client) artifactRequest(req *http.Request) (*http.Response, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, fmt.Errorf("%w: go-judge artifact request", ErrInfrastructure)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: go-judge artifact status %d", ErrInfrastructure, resp.StatusCode)
	}
	return resp, nil
}

func validFileID(value string) bool {
	if len(value) != 8 {
		return false
	}
	for _, r := range value {
		if (r < 'A' || r > 'Z') && (r < '2' || r > '7') {
			return false
		}
	}
	return true
}
