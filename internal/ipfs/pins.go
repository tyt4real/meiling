package ipfs

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
	"meiling/internal/metrics"
	"meiling/internal/validation"
)

type Pin struct {
	CID     string            `json:"cid"`
	Name    string            `json:"name,omitempty"`
	Origins []string          `json:"origins,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

type PinStatus struct {
	RequestID string     `json:"requestid"`
	Status    string     `json:"status"`
	Created   string     `json:"created"`
	Pin       Pin        `json:"pin"`
	Delegates []string   `json:"delegates"`
	Info      *StatusInfo `json:"info,omitempty"`
}

type StatusInfo struct {
	Size string `json:"size,omitempty"`
}

type Failure struct {
	Error FailureError `json:"error"`
}

type FailureError struct {
	Reason  string `json:"reason"`
	Details string `json:"details"`
}

type API struct {
	Pool       *pgxpool.Pool
	Distributor *Distributor
	Reg        *metrics.Registry
}

func (a *API) ListPins(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID) {
	statuses := parseStatuses(r.URL.Query().Get("status"))
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	var before, after time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		before, _ = time.Parse(time.RFC3339, v)
	}
	if v := r.URL.Query().Get("after"); v != "" {
		after, _ = time.Parse(time.RFC3339, v)
	}
	pins, total, err := db.ListPinsWithReplicas(r.Context(), a.Pool, bucketID, statuses, before, after, limit)
	if err != nil {
		http.Error(w, "failed to list pins", http.StatusInternalServerError)
		return
	}
	results := make([]PinStatus, 0, len(pins))
	for _, p := range pins {
		results = append(results, pinToStatus(p))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"count":   total,
		"results": results,
	})
}

func (a *API) AddPin(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID) {
	var p Pin
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeFailure(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body.")
		return
	}
	if err := validation.ValidateCID(p.CID); err != nil {
		writeFailure(w, http.StatusBadRequest, "INVALID_CID", err.Error())
		return
	}
	name := p.Name
	if name == "" {
		name = p.CID
	}
	requestID := uuid.New().String()

	nodeIDs, err := a.Distributor.SelectNodes(r.Context(), 2)
	if err != nil {
		writeFailure(w, http.StatusInsufficientStorage, "NOT_ENOUGH_NODES", err.Error())
		return
	}
	pin, err := db.CreateIPFSPinWithReplicas(r.Context(), a.Pool, bucketID, requestID, p.CID, name, "queued", "0", 2, nodeIDs)
	if err != nil {
		writeFailure(w, http.StatusConflict, "CONFLICT", "Duplicate Name")
		return
	}

	if err := a.Distributor.DistributePin(r.Context(), pin.ID, p.CID); err != nil {
		writeFailure(w, http.StatusInternalServerError, "DISTRIBUTE_FAILED", err.Error())
		return
	}

	a.Reg.PinsTotal.Inc(1)
	a.Reg.PinStatusCounter("queued").Inc(1)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(PinStatus{
		RequestID: requestID,
		Status:    "queued",
		Created:   time.Now().UTC().Format(time.RFC3339),
		Pin:       p,
		Delegates: []string{},
		Info:      &StatusInfo{Size: "0"},
	})
}

func (a *API) GetPin(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, requestID string) {
	p, err := db.GetPinByRequestID(r.Context(), a.Pool, bucketID, requestID)
	if err != nil {
		writeFailure(w, http.StatusNotFound, "NOT_FOUND", "The specified resource was not found")
		return
	}
	if replicas, err := db.ListReplicasForPin(r.Context(), a.Pool, p.ID); err == nil && len(replicas) > 0 {
		p.Status = aggregateReplicaStatus(replicas)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pinToStatus(*p))
}

func (a *API) ReplacePin(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, requestID string) {
	var p Pin
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeFailure(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body.")
		return
	}
	if err := validation.ValidateCID(p.CID); err != nil {
		writeFailure(w, http.StatusBadRequest, "INVALID_CID", err.Error())
		return
	}

	oldPin, err := db.GetPinByRequestID(r.Context(), a.Pool, bucketID, requestID)
	if err != nil {
		writeFailure(w, http.StatusNotFound, "NOT_FOUND", "The specified resource was not found")
		return
	}

	replicas, err := db.ListReplicasForPin(r.Context(), a.Pool, oldPin.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, rep := range replicas {
		if client, ok := a.Distributor.Clients[rep.NodeID]; ok {
			_ = client.PinRemove(oldPin.CID)
		}
	}
	if err := db.DeletePin(r.Context(), a.Pool, bucketID, requestID); err != nil {
		writeFailure(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}

	name := p.Name
	if name == "" {
		name = p.CID
	}
	newID := uuid.New().String()
	nodeIDs, err := a.Distributor.SelectNodes(r.Context(), 2)
	if err != nil {
		writeFailure(w, http.StatusInsufficientStorage, "NOT_ENOUGH_NODES", err.Error())
		return
	}
	newPin, err := db.CreateIPFSPinWithReplicas(r.Context(), a.Pool, bucketID, newID, p.CID, name, "queued", "0", 2, nodeIDs)
	if err != nil {
		writeFailure(w, http.StatusConflict, "CONFLICT", "Duplicate Name")
		return
	}
	if err := a.Distributor.DistributePin(r.Context(), newPin.ID, p.CID); err != nil {
		writeFailure(w, http.StatusInternalServerError, "DISTRIBUTE_FAILED", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(PinStatus{
		RequestID: newID,
		Status:    "queued",
		Created:   time.Now().UTC().Format(time.RFC3339),
		Pin:       p,
		Delegates: []string{},
		Info:      &StatusInfo{Size: "0"},
	})
}

func (a *API) DeletePin(w http.ResponseWriter, r *http.Request, bucketID uuid.UUID, requestID string) {
	found, err := db.GetPinByRequestID(r.Context(), a.Pool, bucketID, requestID)
	if err != nil {
		writeFailure(w, http.StatusNotFound, "NOT_FOUND", "The specified resource was not found")
		return
	}
	replicas, err := db.ListReplicasForPin(r.Context(), a.Pool, found.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	for _, rep := range replicas {
		if client, ok := a.Distributor.Clients[rep.NodeID]; ok {
			_ = client.PinRemove(found.CID)
		}
	}
	if err := db.DeletePin(r.Context(), a.Pool, bucketID, requestID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func pinToStatus(p db.IPFSPin) PinStatus {
	name := p.CID
	if p.Name.Valid {
		name = p.Name.String
	}
	return PinStatus{
		RequestID: p.RequestID,
		Status:    p.Status,
		Created:   p.CreatedAt.UTC().Format(time.RFC3339),
		Pin: Pin{
			CID:  p.CID,
			Name: name,
			Meta: map[string]string{},
		},
		Delegates: []string{},
		Info:      &StatusInfo{Size: p.Size},
	}
}

func writeFailure(w http.ResponseWriter, status int, reason, details string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Failure{
		Error: FailureError{Reason: reason, Details: details},
	})
}

func parseStatuses(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, v := range strings.Split(s, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func aggregateReplicaStatus(replicas []db.IPFSPinReplica) string {
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
		return "failed"
	}
	if allPinned {
		return "pinned"
	}
	return "pinning"
}
