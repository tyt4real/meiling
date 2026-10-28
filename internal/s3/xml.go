package s3

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Envelope struct {
	XMLName xml.Name `xml:"https://s3.amazonaws.com/doc/2006-03-01/ ListBucketResult"`
}

type ListBucketResult struct {
	XMLName     xml.Name `xml:"ListBucketResult"`
	BucketName  string   `xml:"Name"`
	Prefix      string   `xml:"Prefix,omitempty"`
	Marker      string   `xml:"Marker,omitempty"`
	MaxKeys     int      `xml:"MaxKeys,omitempty"`
	IsTruncated bool     `xml:"IsTruncated"`
	Contents    []Object `xml:"Contents"`
}

type Object struct {
	Key          string    `xml:"Key"`
	LastModified string    `xml:"LastModified"`
	ETag         string    `xml:"ETag"`
	Size         int64     `xml:"Size"`
	StorageClass string    `xml:"StorageClass"`
	Owner        *Owner    `xml:"Owner,omitempty"`
}

type Owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type CreateBucketConfiguration struct {
	XMLName xml.Name `xml:"CreateBucketConfiguration"`
	LocationConstraint string `xml:"LocationConstraint,omitempty"`
}

type ListAllMyBucketsResult struct {
	XMLName   xml.Name   `xml:"ListAllMyBucketsResult"`
	Owner     *Owner     `xml:"Owner"`
	Buckets   []Bucket   `xml:"Buckets>Bucket"`
}

type Bucket struct {
	Name         string    `xml:"Name"`
	CreationDate string    `xml:"CreationDate"`
}

type Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

type HeadBucketOutput struct{}

type CreateBucketOutput struct{}

func xmlResponse(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := xml.NewEncoder(w)
	_ = enc.Encode(v)
}

func errorResponse(w http.ResponseWriter, status int, code, message, resource string) {
	xmlResponse(w, status, Error{
		Code:      code,
		Message:   message,
		Resource:  resource,
		RequestID: reqID(),
	})
}

func reqID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

func isoTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

var _ = io.Discard
var _ = strings.TrimSpace
