package main

// Minimal S3 client (PUT/LIST/DELETE) with SigV4 signing — enough for panel
// backups without pulling in the AWS SDK. Works with AWS and S3-compatible
// endpoints (path-style when an endpoint is set).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func hmac256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// s3Request signs and sends one request. key is the object key ("" for bucket
// operations); query is a raw, already-encoded query string like "list-type=2".
func s3Request(method, key, query string, body []byte) (*http.Response, error) {
	settingsMu.Lock()
	bucket, region, akid, secret, endpoint := settings.S3Bucket, settings.S3Region, settings.S3Key, settings.S3Secret, settings.S3Endpoint
	settingsMu.Unlock()
	if bucket == "" || akid == "" || secret == "" {
		return nil, fmt.Errorf("S3 storage not configured")
	}
	if region == "" {
		region = "us-east-1"
	}

	var host, path string
	if endpoint != "" { // path-style for MinIO/B2/Wasabi/...
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("bad S3 endpoint %q", endpoint)
		}
		host = u.Host
		path = "/" + bucket
	} else {
		host = bucket + ".s3." + region + ".amazonaws.com"
		path = ""
	}
	if key != "" {
		path += "/" + strings.TrimPrefix(key, "/")
	}
	if path == "" {
		path = "/"
	}

	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	scope := now.Format("20060102") + "/" + region + "/s3/aws4_request"
	payloadHash := sha256hex(body)

	canonical := strings.Join([]string{
		method,
		(&url.URL{Path: path}).EscapedPath(),
		query,
		"host:" + host,
		"x-amz-content-sha256:" + payloadHash,
		"x-amz-date:" + amzDate,
		"",
		"host;x-amz-content-sha256;x-amz-date",
		payloadHash,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, sha256hex([]byte(canonical)),
	}, "\n")
	k := hmac256([]byte("AWS4"+secret), []byte(now.Format("20060102")))
	k = hmac256(k, []byte(region))
	k = hmac256(k, []byte("s3"))
	k = hmac256(k, []byte("aws4_request"))
	sig := hex.EncodeToString(hmac256(k, []byte(stringToSign)))

	rawURL := "https://" + host + path
	if query != "" {
		rawURL += "?" + query
	}
	req, err := http.NewRequest(method, rawURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential="+akid+"/"+scope+
			", SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature="+sig)
	client := &http.Client{Timeout: 5 * time.Minute}
	return client.Do(req)
}

func s3Err(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("S3 returned %s: %s", resp.Status, strings.TrimSpace(string(b)))
}

func s3Put(key string, body []byte) error {
	resp, err := s3Request("PUT", key, "", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return s3Err(resp)
	}
	return nil
}

func s3List(prefix string) ([]string, error) {
	resp, err := s3Request("GET", "", "list-type=2&prefix="+url.QueryEscape(prefix), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, s3Err(resp)
	}
	var out struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(out.Contents))
	for _, c := range out.Contents {
		keys = append(keys, c.Key)
	}
	sort.Strings(keys) // timestamped names → oldest first
	return keys, nil
}

func s3Delete(key string) error {
	resp, err := s3Request("DELETE", key, "", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		return s3Err(resp)
	}
	return nil
}
