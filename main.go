package webflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Param for http get parameter
type Param struct {
	Page    int
	PerPage int
}

const (
	// host is the default host of Webflow's API.
	host = "https://api.webflow.com"
	// defaultVersion is the default version used for API requests.
	defaultVersion = "1.0.0"
	// defaultTimeout is the default timeout duration used on HTTP requests.
	defaultTimeout = 5 * time.Second
	// defaultCode is the default error code for failures.
	defaultCode = -1
)

var (
	// ErrorMissingTokenOrVersion for missing config
	ErrorMissingTokenOrVersion = errors.New("missing webflow token or version")
)

// fileOpener defines the methods needed to support file uploads.
type fileOpener interface {
	Open(name string) (io.ReadCloser, error)
}

// Error defines an error received when making a request to the API.
type Error struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// Webflow defines the Webflow client.
type Webflow struct {
	AccessToken string
	Host        string
	Version     string
	Debug       bool
	Timeout     time.Duration
	Transport   http.RoundTripper
	RateLimit   int
	Remaining   int
	fs          fileOpener
}

// Error returns a string representing the error, satisfying the error interface.
func (e Error) Error() string {
	return fmt.Sprintf("Webflow: %s (%d)", e.Message, e.Code)
}

// NewClient returns a new Webflow API client which can be used to make RPC requests.
func NewClient(secret string) (*Webflow, error) {
	if secret == "" {
		return nil, errors.New("missing webflow authentication token")
	}
	return &Webflow{
		AccessToken: secret,
		Host:        host,
		Version:     defaultVersion,
		Debug:       false,
		Timeout:     defaultTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   60 * time.Second,
				KeepAlive: 10 * time.Second,
				DualStack: false,
			}).DialContext,
			MaxIdleConns:        5,
			MaxIdleConnsPerHost: 5,
			IdleConnTimeout:     -1,
			DisableCompression:  false,
			DisableKeepAlives:   false,
		},
		fs: osFS{},
	}, nil
}

// generateJSONRequestData returns the body and content type for a JSON request.
func (m *Webflow) generateJSONRequestData(cr clientRequest) ([]byte, string, error) {
	body, err := json.Marshal(cr.data)
	if err != nil {
		return nil, "", Error{fmt.Sprintf("Could not marshal JSON: %s", err), defaultCode}
	}
	return body, "application/json", nil
}

// request makes a request to Webflow's API
func (m *Webflow) request(cr clientRequest, result interface{}) error {
	body, ct, err := m.generateJSONRequestData(cr)
	if err != nil {
		return err
	}
	// Construct the request
	req, err := http.NewRequest(cr.method, m.Host+cr.path, bytes.NewReader(body))
	if err != nil {
		return Error{fmt.Sprintf("Could not create request: %s", err), defaultCode}
	}
	req.Header.Add("Content-Type", ct)
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Accept-Charset", "utf-8")
	req.Header.Add("Accept-Version", m.Version)
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", m.AccessToken))

	// Create the HTTP client
	client := &http.Client{
		Transport: m.Transport,
		Timeout:   m.Timeout,
	}
	// Make the request
	res, err := client.Do(req)
	if err != nil {
		return Error{fmt.Sprintf("Failed to make request: %s", err), defaultCode}
	}
	defer res.Body.Close()

	m.RateLimit, err = strconv.Atoi(res.Header["x-ratelimit-limit"][0])
	if err != nil {
		return Error{fmt.Sprintf("Failed to parse x-ratelimit-limit: %s", err), defaultCode}
	}
	m.Remaining, err = strconv.Atoi(res.Header["x-ratelimit-remaining"][0])
	if err != nil {
		return Error{fmt.Sprintf("Failed to parse x-ratelimit-remaining: %s", err), defaultCode}
	}

	// Parse the response
	c, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return Error{fmt.Sprintf("Could not read response: %s", err), defaultCode}
	}

	var env envelope
	if err := json.Unmarshal(c, &env); err != nil {
		return Error{fmt.Sprintf("Could not parse response: %s", err), defaultCode}
	}

	if http.StatusOK <= res.StatusCode && res.StatusCode < http.StatusMultipleChoices {
		if env.Data != nil {
			c, _ = json.Marshal(env.Data)
		}
		return json.Unmarshal(c, &result)
	}
	e := env.Errors[0]
	return Error{e.Message, e.Code}
}

// payload defines a struct to represent payloads that are returned from Medium.
type envelope struct {
	Limit     int32
	Remaining int32
	Data      interface{} `json:"data"`
	Errors    []Error     `json:"errors,omitempty"`
}

// clientRequest defines information that can be used to make a request to Webflow.
type clientRequest struct {
	method string
	path   string
	data   interface{}
}

// osFS is an implementation of fileOpener that uses the disk.
type osFS struct{}

// Open opens a file from disk.
func (osFS) Open(name string) (io.ReadCloser, error) { return os.Open(name) }

// requestDataGenerator defines a function that can generate request data.
type requestDataGenerator func(cr clientRequest) ([]byte, string, error)

// Borrowed from multipart/writer.go
var quoteEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"")

// escapeQuotes returns the supplied string with quotes escaped.
func escapeQuotes(s string) string {
	return quoteEscaper.Replace(s)
}
