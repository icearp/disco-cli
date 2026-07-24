package gcp

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// Resolver Wave R24 (continued — see compute_networking_resolvers.go header):
// Reservation and FutureReservation, the 2 remaining orphans in
// `compute_reservation_scanners.go` (ReservationBlock/ReservationSubBlock are
// `Leaf: true` already — no outbound fields per that file's own header).
//
// Reservation.Commitment / LinkedCommitments[] are full self-link URLs
// (verified via `go doc`: "Output only... Full or partial URL to a parent
// commitment"), exact-matched against RegionCommitment's own NativeID —
// same-API field, same convention as the networking resolvers above.
//
// FutureReservation.CommitmentInfo.CommitmentName is, by contrast, a bare
// commitment name (no URL, per `go doc
// compute.FutureReservationCommitmentInfo`) — matched via `bareNameIndex`,
// same bare-name-uniqueness tradeoff already accepted elsewhere in this
// package (e.g. CloudRunSvc in serverless_resolvers.go) since Commitments are
// regional and a cross-region same-name collision is a rare, pre-existing
// class of imprecision, not a new one introduced here.
func init() {
	registerResolver(resolveReservationRelationships,
		EdgeDecl{TypeComputeReservation, TypeComputeRegionCommitment, store.RelUses},
	)
	registerResolver(resolveFutureReservationRelationships,
		EdgeDecl{TypeComputeFutureReservation, TypeComputeRegionCommitment, store.RelUses},
	)
}

func resolveReservationRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeReservation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedCommitments, err := scannedIDSet(p, st, TypeComputeRegionCommitment)
	if err != nil {
		return err
	}
	if len(scannedCommitments) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Commitment        string   `json:"commitment"`
			LinkedCommitments []string `json:"linkedCommitments"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scannedCommitments, r.ID, "gcp", p.ID, TypeComputeRegionCommitment, attrs.Commitment, store.RelUses); err != nil {
			return fmt.Errorf("upsert reservation→commitment: %w", err)
		}
		for _, c := range attrs.LinkedCommitments {
			if err := upsertIfScanned(st, scannedCommitments, r.ID, "gcp", p.ID, TypeComputeRegionCommitment, c, store.RelUses); err != nil {
				return fmt.Errorf("upsert reservation→linkedCommitment: %w", err)
			}
		}
	}
	return nil
}

func resolveFutureReservationRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComputeFutureReservation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	commitmentByName, err := bareNameIndex(p, st, TypeComputeRegionCommitment)
	if err != nil {
		return err
	}
	if len(commitmentByName) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			CommitmentInfo *struct {
				CommitmentName string `json:"commitmentName"`
			} `json:"commitmentInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.CommitmentInfo == nil || attrs.CommitmentInfo.CommitmentName == "" {
			continue
		}
		toID, ok := commitmentByName[attrs.CommitmentInfo.CommitmentName]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert futureReservation→commitment: %w", err)
		}
	}
	return nil
}
