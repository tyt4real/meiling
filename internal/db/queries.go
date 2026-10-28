package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool = pgxpool.Pool

type Account struct {
	ID        uuid.UUID
	Email     string
	CreatedAt time.Time
}

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

type Object struct {
	ID           uuid.UUID
	BucketID     uuid.UUID
	Key          string
	DataPath     string
	Size         int64
	ContentType  sql.NullString
	ETag         string
	CreatedAt    time.Time
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

func CreateAccount(ctx context.Context, q *pgxpool.Pool, email string) (*Account, error) {
	id := uuid.New()
	var a Account
	err := q.QueryRow(ctx,
		`INSERT INTO accounts (id, email) VALUES ($1, $2)
		 RETURNING id, email, created_at`,
		id, email).Scan(&a.ID, &a.Email, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func GetAccountByEmail(ctx context.Context, q *pgxpool.Pool, email string) (*Account, error) {
	var a Account
	err := q.QueryRow(ctx, `SELECT id, email, created_at FROM accounts WHERE email=$1`, email).
		Scan(&a.ID, &a.Email, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
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

func DeleteBucket(ctx context.Context, q *pgxpool.Pool, name string) error {
	_, err := q.Exec(ctx, `DELETE FROM buckets WHERE name=$1`, name)
	return err
}

func GetBucketLocation(ctx context.Context, q *pgxpool.Pool, name string) (string, error) {
	var loc string
	err := q.QueryRow(ctx, `SELECT 'auto'`).Scan(&loc)
	return loc, err
}

func CreateObject(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, key, dataPath string, size int64, contentType string, etag string) (*Object, error) {
	id := uuid.New()
	var o Object
	err := q.QueryRow(ctx,
		`INSERT INTO objects (id, bucket_id, key, data_path, size, content_type, etag)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 RETURNING id, bucket_id, key, data_path, size, content_type, etag, created_at`,
		id, bucketID, key, dataPath, size, contentType, etag).
		Scan(&o.ID, &o.BucketID, &o.Key, &o.DataPath, &o.Size, &o.ContentType, &o.ETag, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func GetObjectByKey(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, key string) (*Object, error) {
	var o Object
	err := q.QueryRow(ctx,
		`SELECT id, bucket_id, key, data_path, size, content_type, etag, created_at
		 FROM objects WHERE bucket_id=$1 AND key=$2`, bucketID, key).
		Scan(&o.ID, &o.BucketID, &o.Key, &o.DataPath, &o.Size, &o.ContentType, &o.ETag, &o.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func ListObjects(ctx context.Context, q *pgxpool.Pool, bucketID uuid.UUID, prefix, marker string, limit int) ([]Object, error) {
	query := `SELECT id, bucket_id, key, data_path, size, content_type, etag, created_at
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
		if err := rows.Scan(&o.ID, &o.BucketID, &o.Key, &o.DataPath, &o.Size, &o.ContentType, &o.ETag, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
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

