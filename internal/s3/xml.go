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

// Multipart upload XML types.

type InitiateMultipartUploadResult struct {
	XMLName xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket  string   `xml:"Bucket"`
	Key     string   `xml:"Key"`
	UploadId string  `xml:"UploadId"`
}

type Part struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type CompleteMultipartUpload struct {
	XMLName xml.Name `xml:"CompleteMultipartUpload"`
	Parts   []Part   `xml:"Part"`
}

type CompleteMultipartUploadResult struct {
	XMLName      xml.Name `xml:"CompleteMultipartUploadResult"`
	Location     string   `xml:"Location"`
	Bucket       string   `xml:"Bucket"`
	Key          string   `xml:"Key"`
	ETag         string   `xml:"ETag"`
	LastModified string   `xml:"LastModified,omitempty"`
}

type CopyObjectResult struct {
	XMLName      xml.Name `xml:"CopyObjectResult"`
	LastModified string   `xml:"LastModified"`
	ETag         string   `xml:"ETag"`
}

// Delete objects XML types.

type DeleteObjectsRequest struct {
	XMLName xml.Name `xml:"Delete"`
	Quiet   *bool    `xml:"Quiet,omitempty"`
	Objects []ObjectIdentifier `xml:"Object"`
}

type ObjectIdentifier struct {
	Key string `xml:"Key"`
}

type DeletedObject struct {
	Key string `xml:"Key"`
}

type DeleteResult struct {
	XMLName xml.Name `xml:"DeleteResult"`
	Deleted []DeletedObject `xml:"Deleted,omitempty"`
}

// List multipart uploads XML types.

type ListMultipartUploadsResult struct {
	XMLName         xml.Name `xml:"ListMultipartUploadsResult"`
	Bucket          string   `xml:"Bucket"`
	KeyMarker       string   `xml:"KeyMarker,omitempty"`
	UploadIdMarker  string   `xml:"UploadIdMarker,omitempty"`
	NextKeyMarker   string   `xml:"NextKeyMarker,omitempty"`
	NextUploadIdMarker string  `xml:"NextUploadIdMarker,omitempty"`
	MaxKeys         int      `xml:"MaxKeys,omitempty"`
	IsTruncated     bool     `xml:"IsTruncated"`
	Uploads         []Upload `xml:"Upload,omitempty"`
}

type Upload struct {
	UploadId   string    `xml:"UploadId"`
	Key        string    `xml:"Key"`
	Initiated  string    `xml:"Initiated"`
	StorageClass string  `xml:"StorageClass,omitempty"`
	Owner      *Owner    `xml:"Owner,omitempty"`
}

// List parts XML types.

type ListPartsResult struct {
	XMLName           xml.Name `xml:"ListPartsResult"`
	Bucket            string   `xml:"Bucket"`
	Key               string   `xml:"Key"`
	UploadId          string   `xml:"UploadId"`
	PartNumberMarker  int      `xml:"PartNumberMarker,omitempty"`
	NextPartNumberMarker int    `xml:"NextPartNumberMarker,omitempty"`
	MaxParts          int      `xml:"MaxParts,omitempty"`
	IsTruncated       bool     `xml:"IsTruncated"`
	Parts             []ListPart `xml:"Part,omitempty"`
}

type ListPart struct {
	PartNumber   int    `xml:"PartNumber"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	Owner        *Owner `xml:"Owner,omitempty"`
}

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
