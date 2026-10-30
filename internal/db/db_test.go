package db_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
)

var dbtestPool *pgxpool.Pool

func TestMain(m *testing.M) {
	var err error
	dbURL := os.Getenv("TEST_DB_URL")
	if dbURL == "" {
		dbURL = "postgres://s3ipfs:s3ipfs@localhost:5432/s3ipfs?sslmode=disable"
	}
	dbtestPool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		panic(err)
	}
	defer dbtestPool.Close()
	code := m.Run()
	os.Exit(code)
}

func TestAccountAndBucketCRUD(t *testing.T) {
	ctx := context.Background()
	acc, err := db.CreateAccount(ctx, dbtestPool, db.RoleUser, "crud-"+uuid.New().String()[:8]+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if acc.ID == uuid.Nil {
		t.Error("expected account id")
	}

	// Get by email
	got, err := db.GetAccountByEmail(ctx, dbtestPool, acc.Email)
	if err != nil || got.ID != acc.ID {
		t.Fatal("get account by email failed")
	}

	b, err := db.CreateBucket(ctx, dbtestPool, acc.ID, "crud-bucket-"+uuid.New().String()[:8])
	if err != nil {
		t.Fatal(err)
	}

	b2, err := db.GetBucketByName(ctx, dbtestPool, b.Name)
	if err != nil || b2.ID != b.ID {
		t.Fatal("get bucket by name failed")
	}

	buckets, err := db.ListBuckets(ctx, dbtestPool, acc.ID)
	if err != nil || len(buckets) < 1 {
		t.Fatal("list buckets failed")
	}

	if err := db.DeleteBucket(ctx, dbtestPool, b.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetBucketByName(ctx, dbtestPool, b.Name); err == nil {
		t.Error("expected bucket to be deleted")
	}
}

func TestAccessKeyCRUD(t *testing.T) {
	ctx := context.Background()
	acc, err := db.CreateAccount(ctx, dbtestPool, db.RoleUser, "key-"+uuid.New().String()[:8]+"@example.com")
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.CreateBucket(ctx, dbtestPool, acc.ID, "key-bucket-"+uuid.New().String()[:8])
	if err != nil {
		t.Fatal(err)
	}
	bid := b.ID
	ak := "testkey-" + uuid.New().String()[:8]
	_, err = db.CreateAccessKey(ctx, dbtestPool, acc.ID, &bid, ak, "testsecret456")
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAccessKey(ctx, dbtestPool, ak)
	if err != nil || got.SecretAccessKey != "testsecret456" {
		t.Fatal("get access key failed")
	}
	if got.BucketID == nil || *got.BucketID != b.ID {
		t.Error("expected bucket id on access key")
	}
}
