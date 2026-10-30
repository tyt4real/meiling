package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool = pgxpool.Pool

type Account struct {
	ID        uuid.UUID
	Email     string
	Role      string
	CreatedAt time.Time
}

// IsAdmin returns true if the account has the admin role.
func (a *Account) IsAdmin() bool {
	return a.Role == RoleAdmin
}

// IsUser returns true if the account has the user role.
func (a *Account) IsUser() bool {
	return a.Role == RoleUser || a.Role == ""
}

// RoleAdmin is the admin role constant.
const RoleAdmin = "admin"

// RoleUser is the user role constant.
const RoleUser = "user"

type AccessKey struct {
	ID               uuid.UUID
	AccountID        uuid.UUID
	BucketID         *uuid.UUID
	AccessKeyID      string
	SecretAccessKey  string
	Status           string
	CreatedAt        time.Time
}

type Bucket struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	Name      string
	CreatedAt time.Time
}

type IPFSPin struct {
	ID            uuid.UUID
	BucketID      uuid.UUID
	RequestID     string
	CID           string
	Name          sql.NullString
	Status        string
	Size          string
	ReplicaFactor int
	CreatedAt     time.Time
}

type IPFSNode struct {
	ID             uuid.UUID
	PeerID         sql.NullString
	APIEndpoint    string
	Status         string
	LastSeen       sql.NullTime
	CapacityUsed   int64
	CapacityTotal  int64
	CreatedAt      time.Time
}

type IPFSPinReplica struct {
	ID        uuid.UUID
	PinID     uuid.UUID
	NodeID    uuid.UUID
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func Connect(ctx context.Context, url string) (*Pool, error) {
	return pgxpool.New(ctx, url)
}

func CreateAccount(ctx context.Context, q *pgxpool.Pool, email string, role string) (*Account, error) {
	id := uuid.New()
	var a Account
	err := q.QueryRow(ctx,
		`INSERT INTO accounts (id, email, role) VALUES ($1, $2, $3)
		 RETURNING id, email, role, created_at`,
		id, email, role).Scan(&a.ID, &a.Email, &a.Role, &a.CreatedAt)
	if err != nil {
		// If email already exists, return the existing account
		var existing Account
		err2 := q.QueryRow(ctx,
			`SELECT id, email, role, created_at FROM accounts WHERE email=$1`,
			email).Scan(&existing.ID, &existing.Email, &existing.Role, &existing.CreatedAt)
		if err2 == nil {
			return &existing, nil
		}
		return nil, err
	}
	return &a, nil
}

func GetAccountByEmail(ctx context.Context, q *pgxpool.Pool, email string) (*Account, error) {
	var a Account
	err := q.QueryRow(ctx, `SELECT id, email, role, created_at FROM accounts WHERE email=$1`, email).
		Scan(&a.ID, &a.Email, &a.Role, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// GetAccountByID returns an account by its ID.
func GetAccountByID(ctx context.Context, q *pgxpool.Pool, id uuid.UUID) (*Account, error) {
	var a Account
	err := q.QueryRow(ctx, `SELECT id, email, role, created_at FROM accounts WHERE id=$1`, id).
		Scan(&a.ID, &a.Email, &a.Role, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// ListAccounts returns all accounts, ordered by created_at.
func ListAccounts(ctx context.Context, q *pgxpool.Pool) ([]Account, error) {
	rows, err := q.Query(ctx, `SELECT id, email, role, created_at FROM accounts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Email, &a.Role, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAccountRole updates an account's role.
func UpdateAccountRole(ctx context.Context, q *pgxpool.Pool, id uuid.UUID, role string) error {
	_, err := q.Exec(ctx, `UPDATE accounts SET role=$1 WHERE id=$2`, role, id)
	return err
}

func CreateAccessKey(ctx context.Context, q *pgxpool.Pool, accountID uuid.UUID, bucketID *uuid.UUID, accessKeyID, secretAccessKey string) (*AccessKey, error) {
	id := uuid.New()
	var k AccessKey
	var bid interface{}
	if bucketID != nil {
		bid = *bucketID
	}
	err := q.QueryRow(ctx,
		`INSERT INTO access_keys (id, account_id, bucket_id, access_key_id, secret_access_key)
		 VALUES ($1,$2,$3,$4,$5)
		 RETURNING id, account_id, bucket_id, access_key_id, secret_access_key, status, created_at`,
		id, accountID, bid, accessKeyID, secretAccessKey).
		Scan(&k.ID, &k.AccountID, &k.BucketID, &k.AccessKeyID, &k.SecretAccessKey, &k.Status, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func GetAccessKey(ctx context.Context, q *pgxpool.Pool, accessKeyID string) (*AccessKey, error) {
	var k AccessKey
	err := q.QueryRow(ctx,
		`SELECT id, account_id, bucket_id, access_key_id, secret_access_key, status, created_at
		 FROM access_keys WHERE access_key_id=$1`, accessKeyID).
		Scan(&k.ID, &k.AccountID, &k.BucketID, &k.AccessKeyID, &k.SecretAccessKey, &k.Status, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

func CreateBucket(ctx context.Context, q *pgxpool.Pool, accountID uuid.UUID, name string) (*Bucket, error) {
	id := uuid.New()
	var b Bucket
	err := q.QueryRow(ctx,
		`INSERT INTO buckets (id, account_id, name) VALUES ($1,$2,$3)
		 RETURNING id, account_id, name, created_at`,
		id, accountID, name).Scan(&b.ID, &b.AccountID, &b.Name, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func GetBucketByName(ctx context.Context, q *pgxpool.Pool, name string) (*Bucket, error) {
	var b Bucket
	err := q.QueryRow(ctx, `SELECT id, account_id, name, created_at FROM buckets WHERE name=$1`, name).
		Scan(&b.ID, &b.AccountID, &b.Name, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func ListBuckets(ctx context.Context, q *pgxpool.Pool, accountID uuid.UUID) ([]Bucket, error) {
	rows, err := q.Query(ctx, `SELECT id, account_id, name, created_at FROM buckets WHERE account_id=$1 ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.ID, &b.AccountID, &b.Name, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ListAllBuckets lists all buckets (admin only).
func ListAllBuckets(ctx context.Context, q *pgxpool.Pool) ([]Bucket, error) {
	rows, err := q.Query(ctx, `SELECT id, account_id, name, created_at FROM buckets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.ID, &b.AccountID, &b.Name, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func DeleteBucket(ctx context.Context, q *pgxpool.Pool, name string) error {
	_, err := q.Exec(ctx, `DELETE FROM buckets WHERE name=$1`, name)
	return err
}

func GetBucketLocation(ctx context.Context, q *pgxpool.Pool, name string) (string, error) {
	var loc string
	err := q.QueryRow(ctx, `SELECT 'auto'`).Scan(&loc)
	return loc, err
}

func CreateObject(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, key, dataPath string, size int64, contentType string, etag string, cid string, ipfsStatus string, userMetadata map[string]string) (*Object, error) {
	id := uuid.New()
	var o Object
	var cidArg, umArg interface{}
	if cid != "" {
		cidArg = cid
	}
	if userMetadata == nil {
		umArg = []byte("{}")
	} else {
		b, err := json.Marshal(userMetadata)
		if err != nil {
			return nil, err
		}
		umArg = b
	}
	err := q.QueryRow(ctx,
		`INSERT INTO objects (id, bucket_id, key, data_path, size, content_type, etag, cid, ipfs_status, user_metadata)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 RETURNING id, bucket_id, key, data_path, size, content_type, etag, cid, ipfs_status, user_metadata, created_at`,
		id, bucketID, key, dataPath, size, contentType, etag, cidArg, ipfsStatus, umArg).
		Scan(&o.ID, &o.BucketID, &o.Key, &o.DataPath, &o.Size, &o.ContentType, &o.ETag, &o.CID, &o.IPFSStatus, &o.UserMetadata, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func GetObjectByKey(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, key string) (*Object, error) {
	var o Object
	err := q.QueryRow(ctx,
		`SELECT id, bucket_id, key, data_path, size, content_type, etag, cid, ipfs_status, user_metadata, created_at
		 FROM objects WHERE bucket_id=$1 AND key=$2`, bucketID, key).
		Scan(&o.ID, &o.BucketID, &o.Key, &o.DataPath, &o.Size, &o.ContentType, &o.ETag, &o.CID, &o.IPFSStatus, &o.UserMetadata, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func ListObjects(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, prefix, marker string, limit int) ([]Object, error) {
	query := `SELECT id, bucket_id, key, data_path, size, content_type, etag, cid, ipfs_status, user_metadata, created_at
		 FROM objects WHERE bucket_id=$1`
	args := []any{bucketID}
	argIdx := 2
	if prefix != "" {
		query += ` AND key >= $` + string(rune('0'+argIdx))
		args = append(args, prefix)
		argIdx++
	}
	if marker != "" {
		query += ` AND key > $` + string(rune('0'+argIdx))
		args = append(args, marker)
		argIdx++
	}
	query += ` ORDER BY key ASC LIMIT $` + string(rune('0'+argIdx))
	args = append(args, limit)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Object
	for rows.Next() {
		var o Object
		if err := rows.Scan(&o.ID, &o.BucketID, &o.Key, &o.DataPath, &o.Size, &o.ContentType, &o.ETag, &o.CID, &o.IPFSStatus, &o.UserMetadata, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CreateMultipartUpload records a new multipart upload and returns its upload ID.
func CreateMultipartUpload(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, key, contentType string, userMetadata map[string]string) (*MultipartUpload, error) {
	id := uuid.New()
	uploadID := id.String()
	var mu MultipartUpload
	var umArg interface{}
	if userMetadata == nil {
		umArg = []byte("{}")
	} else {
		b, err := json.Marshal(userMetadata)
		if err != nil {
			return nil, err
		}
		umArg = b
	}
	err := q.QueryRow(ctx,
		`INSERT INTO multipart_uploads (id, upload_id, bucket_id, key, content_type, user_metadata)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 RETURNING id, upload_id, bucket_id, key, content_type, user_metadata, created_at`,
		id, uploadID, bucketID, key, contentType, umArg).
		Scan(&mu.ID, &mu.UploadID, &mu.BucketID, &mu.Key, &mu.ContentType, &mu.UserMetadata, &mu.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &mu, nil
}

// GetMultipartUpload returns a multipart upload by upload ID.
func GetMultipartUpload(ctx context.Context, q *pgxpool.Pool, uploadID string) (*MultipartUpload, error) {
	var mu MultipartUpload
	err := q.QueryRow(ctx,
		`SELECT id, upload_id, bucket_id, key, content_type, user_metadata, created_at
		 FROM multipart_uploads WHERE upload_id=$1`, uploadID).
		Scan(&mu.ID, &mu.UploadID, &mu.BucketID, &mu.Key, &mu.ContentType, &mu.UserMetadata, &mu.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &mu, nil
}

// ListMultipartParts returns the parts of a multipart upload ordered by part number.
// Supports partNumberMarker and limit for pagination (0 means no filter).
func ListMultipartParts(ctx context.Context, q *pgxpool.Pool, uploadID string, partNumberMarker int, limit int) ([]MultipartPart, error) {
	query := `SELECT upload_id, part_number, size, etag
		 FROM multipart_parts WHERE upload_id=$1`
	args := []any{uploadID}
	argIdx := 2
	if partNumberMarker > 0 {
		query += ` AND part_number > $` + string(rune('0'+argIdx))
		args = append(args, partNumberMarker)
		argIdx++
	}
	query += ` ORDER BY part_number ASC`
	if limit > 0 {
		query += ` LIMIT $` + string(rune('0'+argIdx))
		args = append(args, limit)
	}
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MultipartPart
	for rows.Next() {
		var p MultipartPart
		if err := rows.Scan(&p.UploadID, &p.PartNumber, &p.Size, &p.ETag); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CreateMultipartPart records a completed part.
func CreateMultipartPart(ctx context.Context, q *pgxpool.Pool, uploadID string, partNumber int, size int64, etag string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO multipart_parts (upload_id, part_number, size, etag)
		 VALUES ($1,$2,$3,$4)
		 ON CONFLICT (upload_id, part_number) DO UPDATE SET size=$3, etag=$4`,
		uploadID, partNumber, size, etag)
	return err
}

// DeleteMultipartUpload removes a multipart upload and all its parts.
func DeleteMultipartUpload(ctx context.Context, q *pgxpool.Pool, uploadID string) error {
	_, err := q.Exec(ctx, `DELETE FROM multipart_uploads WHERE upload_id=$1`, uploadID)
	return err
}

// OldestMultipartUploads returns uploads older than the given cutoff, for janitor cleanup.
func OldestMultipartUploads(ctx context.Context, q *pgxpool.Pool, cutoff time.Time, limit int) ([]MultipartUpload, error) {
	rows, err := q.Query(ctx,
		`SELECT id, upload_id, bucket_id, key, content_type, user_metadata, created_at
		 FROM multipart_uploads WHERE created_at < $1 ORDER BY created_at ASC LIMIT $2`,
		cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MultipartUpload
	for rows.Next() {
		var mu MultipartUpload
		if err := rows.Scan(&mu.ID, &mu.UploadID, &mu.BucketID, &mu.Key, &mu.ContentType, &mu.UserMetadata, &mu.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, mu)
	}
	return out, rows.Err()
}

// ListMultipartUploads lists in-progress multipart uploads for a bucket.
// Supports prefix, key-marker, upload-id-marker, and max-keys for pagination.
func ListMultipartUploads(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, prefix, keyMarker, uploadIDMarker string, limit int) ([]MultipartUpload, error) {
	query := `SELECT id, upload_id, bucket_id, key, content_type, user_metadata, created_at
		 FROM multipart_uploads WHERE bucket_id=$1`
	args := []any{bucketID}
	argIdx := 2
	if prefix != "" {
		query += ` AND key >= $` + string(rune('0'+argIdx))
		args = append(args, prefix)
		argIdx++
	}
	if keyMarker != "" {
		query += ` AND key > $` + string(rune('0'+argIdx))
		args = append(args, keyMarker)
		argIdx++
	}
	if uploadIDMarker != "" {
		query += ` AND upload_id > $` + string(rune('0'+argIdx))
		args = append(args, uploadIDMarker)
		argIdx++
	}
	query += ` ORDER BY key ASC, upload_id ASC LIMIT $` + string(rune('0'+argIdx))
	args = append(args, limit)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MultipartUpload
	for rows.Next() {
		var mu MultipartUpload
		if err := rows.Scan(&mu.ID, &mu.UploadID, &mu.BucketID, &mu.Key, &mu.ContentType, &mu.UserMetadata, &mu.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, mu)
	}
	return out, rows.Err()
}

func DeleteObject(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, key string) error {
	_, err := q.Exec(ctx, `DELETE FROM objects WHERE bucket_id=$1 AND key=$2`, bucketID, key)
	return err
}

func CreateIPFSPin(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, requestID, cid, name, status, size string) (*IPFSPin, error) {
	id := uuid.New()
	var p IPFSPin
	err := q.QueryRow(ctx,
		`INSERT INTO ipfs_pins (id, bucket_id, request_id, cid, name, status, size)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING id, bucket_id, request_id, cid, name, status, size, created_at`,
		id, bucketID, requestID, cid, name, status, size).
		Scan(&p.ID, &p.BucketID, &p.RequestID, &p.CID, &p.Name, &p.Status, &p.Size, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func ListPins(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, statuses []string, before, after time.Time, limit int) ([]IPFSPin, int, error) {
	query := `SELECT id, bucket_id, request_id, cid, name, status, size, created_at FROM ipfs_pins WHERE bucket_id=$1`
	args := []any{bucketID}
	argIdx := 2
	if len(statuses) > 0 {
		query += ` AND status = ANY($` + string(rune('0'+argIdx)) + `)`
		args = append(args, statuses)
		argIdx++
	}
	if !before.IsZero() {
		query += ` AND created_at < $` + string(rune('0'+argIdx))
		args = append(args, before)
		argIdx++
	}
	if !after.IsZero() {
		query += ` AND created_at > $` + string(rune('0'+argIdx))
		args = append(args, after)
		argIdx++
	}
	countQuery := `SELECT COUNT(*) FROM (` + query + `) AS sub`
	var total int
	if err := q.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query += ` ORDER BY created_at DESC LIMIT $` + string(rune('0'+argIdx))
	args = append(args, limit)
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []IPFSPin
	for rows.Next() {
		var p IPFSPin
		if err := rows.Scan(&p.ID, &p.BucketID, &p.RequestID, &p.CID, &p.Name, &p.Status, &p.Size, &p.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

func GetPin(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, requestID string) (*IPFSPin, error) {
	var p IPFSPin
	err := q.QueryRow(ctx,
		`SELECT id, bucket_id, request_id, cid, name, status, size, replica_factor, created_at FROM ipfs_pins WHERE bucket_id=$1 AND request_id=$2`,
		bucketID, requestID).Scan(&p.ID, &p.BucketID, &p.RequestID, &p.CID, &p.Name, &p.Status, &p.Size, &p.ReplicaFactor, &p.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func GetPinByRequestID(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, requestID string) (*IPFSPin, error) {
	return GetPin(ctx, q, bucketID, requestID)
}

func DeletePin(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, requestID string) error {
	_, err := q.Exec(ctx, `DELETE FROM ipfs_pins WHERE bucket_id=$1 AND request_id=$2`, bucketID, requestID)
	return err
}

func CreateIPFSPinWithReplicas(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, requestID, cid, name, status, size string, replicaFactor int, nodeIDs []uuid.UUID) (*IPFSPin, error) {
	tx, err := q.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	id := uuid.New()
	var p IPFSPin
	err = tx.QueryRow(ctx,
		`INSERT INTO ipfs_pins (id, bucket_id, request_id, cid, name, status, size, replica_factor)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id, bucket_id, request_id, cid, name, status, size, replica_factor, created_at`,
		id, bucketID, requestID, cid, name, status, size, replicaFactor).
		Scan(&p.ID, &p.BucketID, &p.RequestID, &p.CID, &p.Name, &p.Status, &p.Size, &p.ReplicaFactor, &p.CreatedAt)
	if err != nil {
		return nil, err
	}

	for _, nodeID := range nodeIDs {
		rid := uuid.New()
		_, err = tx.Exec(ctx,
			`INSERT INTO ipfs_pin_replicas (id, pin_id, node_id, status, created_at, updated_at)
			 VALUES ($1,$2,$3,'queued',$4,$4)`,
			rid, id, nodeID, time.Now())
		if err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

func ListNodes(ctx context.Context, q *pgxpool.Pool) ([]IPFSNode, error) {
	rows, err := q.Query(ctx, `SELECT id, peer_id, api_endpoint, status, last_seen, capacity_used, capacity_total, created_at FROM ipfs_nodes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IPFSNode
	for rows.Next() {
		var n IPFSNode
		if err := rows.Scan(&n.ID, &n.PeerID, &n.APIEndpoint, &n.Status, &n.LastSeen, &n.CapacityUsed, &n.CapacityTotal, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func CreateNode(ctx context.Context, q *pgxpool.Pool, apiEndpoint string, peerID string) (*IPFSNode, error) {
	id := uuid.New()
	var n IPFSNode
	var pid interface{}
	if peerID != "" {
		pid = peerID
	}
	err := q.QueryRow(ctx,
		`INSERT INTO ipfs_nodes (id, peer_id, api_endpoint, status, created_at)
		 VALUES ($1,$2,$3,'healthy',NOW())
		 RETURNING id, peer_id, api_endpoint, status, last_seen, capacity_used, capacity_total, created_at`,
		id, pid, apiEndpoint).
		Scan(&n.ID, &n.PeerID, &n.APIEndpoint, &n.Status, &n.LastSeen, &n.CapacityUsed, &n.CapacityTotal, &n.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func DeleteNode(ctx context.Context, q *pgxpool.Pool, nodeID uuid.UUID) error {
	_, err := q.Exec(ctx, `DELETE FROM ipfs_nodes WHERE id=$1`, nodeID)
	return err
}

func ListReplicasForPin(ctx context.Context, q *pgxpool.Pool, pinID uuid.UUID) ([]IPFSPinReplica, error) {
	rows, err := q.Query(ctx, `SELECT id, pin_id, node_id, status, created_at, updated_at FROM ipfs_pin_replicas WHERE pin_id=$1 ORDER BY created_at ASC`, pinID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IPFSPinReplica
	for rows.Next() {
		var r IPFSPinReplica
		if err := rows.Scan(&r.ID, &r.PinID, &r.NodeID, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func UpdateReplicaStatus(ctx context.Context, q *pgxpool.Pool, replicaID uuid.UUID, status string) error {
	_, err := q.Exec(ctx, `UPDATE ipfs_pin_replicas SET status=$1, updated_at=NOW() WHERE id=$2`, status, replicaID)
	return err
}

func ListPinsWithReplicas(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, statuses []string, before, after time.Time, limit int) ([]IPFSPin, int, error) {
	pins, total, err := ListPins(ctx, q, bucketID, statuses, before, after, limit)
	if err != nil {
		return nil, 0, err
	}
	for i := range pins {
		replicas, err := ListReplicasForPin(ctx, q, pins[i].ID)
		if err != nil {
			return nil, 0, err
		}
		// Determine aggregated status from replicas
		if len(replicas) == 0 {
			continue
		}
		allPinned := true
		anyFailed := false
		for _, r := range replicas {
			if r.Status != "pinned" {
				allPinned = false
			}
			if r.Status == "failed" {
				anyFailed = true
			}
		}
		if anyFailed {
			pins[i].Status = "failed"
		} else if allPinned {
			pins[i].Status = "pinned"
		} else {
			pins[i].Status = "pinning"
		}
	}
	return pins, total, nil
}

// GetBucketIDByName returns the bucket id for a name.
func GetBucketIDByName(ctx context.Context, pool *pgxpool.Pool, name string) (uuid.UUID, error) {
	b, err := GetBucketByName(ctx, pool, name)
	if err != nil {
		return uuid.Nil, err
	}
	return b.ID, nil
}

// GetAccessKeyByAccount returns the first access key for an account.
func GetAccessKeyByAccount(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID) (*AccessKey, error) {
	var k AccessKey
	err := pool.QueryRow(ctx,
		`SELECT id, account_id, bucket_id, access_key_id, secret_access_key, status, created_at
		 FROM access_keys WHERE account_id=$1 LIMIT 1`, accountID).
		Scan(&k.ID, &k.AccountID, &k.BucketID, &k.AccessKeyID, &k.SecretAccessKey, &k.Status, &k.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

