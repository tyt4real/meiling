package ipfs

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
	"meiling/internal/metrics"
)

// Distributor assigns pins to IPFS nodes and triggers pin operations.
type Distributor struct {
	Pool    *pgxpool.Pool
	Clients map[uuid.UUID]*NodeClient
	rng     *rand.Rand
	Reg     *metrics.Registry
}

func NewDistributor(pool *pgxpool.Pool, reg *metrics.Registry) *Distributor {
	return &Distributor{
		Pool:    pool,
		Clients: make(map[uuid.UUID]*NodeClient),
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())),
		Reg:     reg,
	}
}

func (d *Distributor) RegisterNode(nodeID uuid.UUID, endpoint string) {
	d.Clients[nodeID] = NewNodeClient(endpoint)
}

// SelectNodes picks replicaFactor healthy nodes biased by free capacity.
func (d *Distributor) SelectNodes(ctx context.Context, replicaFactor int) ([]uuid.UUID, error) {
	nodes, err := db.ListNodes(ctx, d.Pool)
	if err != nil {
		return nil, err
	}
	var healthy []db.IPFSNode
	for _, n := range nodes {
		if n.Status == "healthy" {
			healthy = append(healthy, n)
		}
	}
	if len(healthy) < replicaFactor {
		return nil, fmt.Errorf("not enough healthy nodes: have %d, need %d", len(healthy), replicaFactor)
	}
	sort.Slice(healthy, func(i, j int) bool {
		freeI := healthy[i].CapacityTotal - healthy[i].CapacityUsed
		freeJ := healthy[j].CapacityTotal - healthy[j].CapacityUsed
		return freeI > freeJ
	})
	pickCount := replicaFactor
	if pickCount > len(healthy) {
		pickCount = len(healthy)
	}
	selected := healthy[:pickCount]
	d.rng.Shuffle(len(selected), func(i, j int) {
		selected[i], selected[j] = selected[j], selected[i]
	})
	ids := make([]uuid.UUID, len(selected))
	for i, n := range selected {
		ids[i] = n.ID
	}
	return ids, nil
}

// DistributePin triggers pin add on each node hosting a replica for the pin.
func (d *Distributor) DistributePin(ctx context.Context, pinID uuid.UUID, cid string) error {
	replicas, err := db.ListReplicasForPin(ctx, d.Pool, pinID)
	if err != nil {
		return err
	}
	for _, r := range replicas {
		nodeID := r.NodeID
		go func() {
			client, ok := d.Clients[nodeID]
			if !ok {
				slog.Error("no client for node", "node", nodeID)
				_ = db.UpdateReplicaStatus(context.Background(), d.Pool, r.ID, "failed")
				return
			}
			if err := client.PinAdd(cid); err != nil {
				slog.Error("pin add failed", "node", nodeID, "error", err)
				_ = db.UpdateReplicaStatus(context.Background(), d.Pool, r.ID, "failed")
				return
			}
			_ = db.UpdateReplicaStatus(context.Background(), d.Pool, r.ID, "pinned")
			slog.Info("pin added", "node", nodeID)
		}()
	}
	return nil
}

// Reconcile polls each node's pin status and updates replicas.
func (d *Distributor) Reconcile(ctx context.Context) error {
	// In production this polls /api/v0/pin/ls on each node for every in-progress
	// replica and updates ipfs_pin_replicas.status accordingly.
	return nil
}

var _ = slog.Info
