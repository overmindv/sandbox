package objectstore

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	s3ServiceName      = "s3"
	aws4Request        = "aws4_request"
	aws4Algorithm      = "AWS4-HMAC-SHA256"
	defaultS3TimeoutMS = 10000
)

type S3Config struct {
	Bucket           string
	Endpoint         string
	Region           string
	AccessKeyID      string
	SecretAccessKey  string
	RequestTimeoutMS int
	ForcePathStyle   bool
}

type S3Store struct {
	bucket          string
	endpoint        *url.URL
	region          string
	accessKeyID     string
	secretAccessKey string
	forcePathStyle  bool
	requestTimeout  time.Duration
	client          *http.Client
	now             func() time.Time
}

func NewS3Store(cfg S3Config) (*S3Store, error) {
	bucket := normalizeBucket(cfg.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("s3 object store bucket is required")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		return nil, fmt.Errorf("s3 object store region is required")
	}
	if strings.TrimSpace(cfg.AccessKeyID) == "" {
		return nil, fmt.Errorf("s3 object store access key id is required")
	}
	if strings.TrimSpace(cfg.SecretAccessKey) == "" {
		return nil, fmt.Errorf("s3 object store secret access key is required")
	}

	timeoutMS := cfg.RequestTimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = defaultS3TimeoutMS
	}

	endpoint, err := parseS3Endpoint(cfg.Endpoint, region)
	if err != nil {
		return nil, err
	}

	return &S3Store{
		bucket:          bucket,
		endpoint:        endpoint,
		region:          region,
		accessKeyID:     strings.TrimSpace(cfg.AccessKeyID),
		secretAccessKey: strings.TrimSpace(cfg.SecretAccessKey),
		forcePathStyle:  cfg.ForcePathStyle,
		requestTimeout:  time.Duration(timeoutMS) * time.Millisecond,
		client:          &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond},
		now:             func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *S3Store) Provider() string {
	return "s3"
}

func (s *S3Store) Put(ctx context.Context, object Object) (StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredObject{}, err
	}
	key, err := NormalizeKey(object.Key)
	if err != nil {
		return StoredObject{}, err
	}
	bucket := normalizeBucket(object.Bucket)
	if bucket == "" {
		bucket = s.bucket
	}
	contentType := NormalizeContentType(object.ContentType)
	payloadHash := Digest(object.Content)

	requestCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	location := s.objectURL(bucket, key)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPut, location.String(), bytes.NewReader(object.Content))
	if err != nil {
		return StoredObject{}, err
	}
	req.ContentLength = int64(len(object.Content))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	s.sign(req, payloadHash, []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"})

	resp, err := s.client.Do(req)
	if err != nil {
		return StoredObject{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return StoredObject{}, fmt.Errorf("s3 put object failed: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return StoredObject{
		Provider:    s.Provider(),
		Bucket:      bucket,
		Key:         key,
		ContentType: contentType,
		SHA256:      payloadHash,
		SizeBytes:   int64(len(object.Content)),
	}, nil
}

func (s *S3Store) Get(ctx context.Context, bucket string, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalizedKey, err := NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	normalizedBucket := normalizeBucket(bucket)
	if normalizedBucket == "" {
		normalizedBucket = s.bucket
	}
	payloadHash := Digest(nil)

	requestCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	location := s.objectURL(normalizedBucket, normalizedKey)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	s.sign(req, payloadHash, []string{"host", "x-amz-content-sha256", "x-amz-date"})

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("s3 get object failed: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

func (s *S3Store) Close() error {
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	return nil
}

func (s *S3Store) objectURL(bucket string, key string) url.URL {
	location := *s.endpoint
	if s.forcePathStyle {
		location.Path = joinURLPath(location.Path, bucket, key)
		return location
	}
	location.Host = bucket + "." + location.Host
	location.Path = joinURLPath(location.Path, key)
	return location
}

func (s *S3Store) sign(req *http.Request, payloadHash string, signedHeaderNames []string) {
	now := s.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)

	sort.Strings(signedHeaderNames)
	canonicalHeaders := strings.Builder{}
	for _, name := range signedHeaderNames {
		switch name {
		case "host":
			canonicalHeaders.WriteString("host:")
			canonicalHeaders.WriteString(req.URL.Host)
			canonicalHeaders.WriteByte('\n')
		default:
			canonicalHeaders.WriteString(name)
			canonicalHeaders.WriteByte(':')
			canonicalHeaders.WriteString(strings.TrimSpace(req.Header.Get(canonicalHeaderKey(name))))
			canonicalHeaders.WriteByte('\n')
		}
	}
	signedHeaders := strings.Join(signedHeaderNames, ";")
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, s.region, s3ServiceName, aws4Request}, "/")
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		aws4Algorithm,
		amzDate,
		scope,
		hex.EncodeToString(canonicalHash[:]),
	}, "\n")
	signature := hex.EncodeToString(hmacSHA256(signingKey(s.secretAccessKey, dateStamp, s.region), []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s", aws4Algorithm, s.accessKeyID, scope, signedHeaders, signature))
}

func parseS3Endpoint(endpoint string, region string) (*url.URL, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return &url.URL{Scheme: "https", Host: fmt.Sprintf("s3.%s.amazonaws.com", region)}, nil
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "https://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("s3 object store endpoint host is required")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func joinURLPath(base string, parts ...string) string {
	values := make([]string, 0, len(parts)+1)
	if strings.TrimSpace(base) != "" {
		values = append(values, strings.Trim(base, "/"))
	}
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			values = append(values, strings.Trim(part, "/"))
		}
	}
	return "/" + path.Join(values...)
}

func canonicalHeaderKey(name string) string {
	switch name {
	case "content-type":
		return "Content-Type"
	case "x-amz-content-sha256":
		return "X-Amz-Content-Sha256"
	case "x-amz-date":
		return "X-Amz-Date"
	default:
		return name
	}
}

func signingKey(secret string, date string, region string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	regionKey := hmacSHA256(dateKey, []byte(region))
	serviceKey := hmacSHA256(regionKey, []byte(s3ServiceName))
	return hmacSHA256(serviceKey, []byte(aws4Request))
}

func hmacSHA256(key []byte, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return mac.Sum(nil)
}
