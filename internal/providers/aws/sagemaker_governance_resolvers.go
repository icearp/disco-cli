package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSageMakerExperimentTrial,
		EdgeDecl{TypeSageMakerExperimentTrial, TypeSageMakerExperiment, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerHubContent,
		EdgeDecl{TypeSageMakerHubContent, TypeSageMakerHub, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerComputeQuota,
		EdgeDecl{TypeSageMakerComputeQuota, TypeSageMakerCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerClusterSchedulerConfig,
		EdgeDecl{TypeSageMakerClusterSchedulerConfig, TypeSageMakerCluster, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerEdgeDeploymentPlan,
		EdgeDecl{TypeSageMakerEdgeDeploymentPlan, TypeSageMakerDeviceFleet, store.RelAttachedTo},
	)
}

// resolveSageMakerExperimentTrial links each trial to its parent experiment via
// the DescribeTrial body's ExperimentName.
func resolveSageMakerExperimentTrial(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerExperimentTrial)
	if err != nil {
		return err
	}
	expSet, err := scannedIDSet(acct, st, TypeSageMakerExperiment)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			ExperimentName *string `json:"ExperimentName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		name := sv(a.ExperimentName)
		if name == "" {
			continue
		}
		expID := store.ResourceID("aws", acct.ID, TypeSageMakerExperiment,
			sagemakerARN(sv(r.Region), acct.ID, "experiment", name))
		if !expSet[expID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, expID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("experiment-trial→experiment: %w", err)
		}
	}
	return nil
}

// resolveSageMakerHubContent links each hub-content row to its parent hub via
// the HubName the scanner embeds (the summary itself omits it).
func resolveSageMakerHubContent(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerHubContent)
	if err != nil {
		return err
	}
	hubSet, err := scannedIDSet(acct, st, TypeSageMakerHub)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			HubName *string `json:"HubName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		name := sv(a.HubName)
		if name == "" {
			continue
		}
		hubID := store.ResourceID("aws", acct.ID, TypeSageMakerHub,
			sagemakerARN(sv(r.Region), acct.ID, "hub", name))
		if !hubSet[hubID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, hubID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("hub-content→hub: %w", err)
		}
	}
	return nil
}

// resolveSageMakerComputeQuota links each compute quota to its HyperPod cluster
// via the summary's ClusterArn (already canonical).
func resolveSageMakerComputeQuota(acct *account, st *store.Store) error {
	return resolveSagemakerClusterArnParent(acct, st, TypeSageMakerComputeQuota, "compute-quota")
}

// resolveSageMakerClusterSchedulerConfig links each scheduler config to its
// HyperPod cluster via the summary's ClusterArn.
func resolveSageMakerClusterSchedulerConfig(acct *account, st *store.Store) error {
	return resolveSagemakerClusterArnParent(acct, st, TypeSageMakerClusterSchedulerConfig, "cluster-scheduler-config")
}

// resolveSagemakerClusterArnParent emits an `attached-to` edge from each row of
// srcType to the cluster named by its top-level ClusterArn. Shared by the
// compute-quota and cluster-scheduler-config resolvers (identical shape).
func resolveSagemakerClusterArnParent(acct *account, st *store.Store, srcType, label string) error {
	rs, err := listSources(acct, st, srcType)
	if err != nil {
		return err
	}
	clusterSet, err := scannedIDSet(acct, st, TypeSageMakerCluster)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			ClusterArn *string `json:"ClusterArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		arn := sv(a.ClusterArn)
		if arn == "" {
			continue
		}
		clusterID := store.ResourceID("aws", acct.ID, TypeSageMakerCluster, arn)
		if !clusterSet[clusterID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, clusterID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("%s→cluster: %w", label, err)
		}
	}
	return nil
}

// resolveSageMakerEdgeDeploymentPlan links each edge deployment plan to the
// device fleet it targets via DeviceFleetName.
func resolveSageMakerEdgeDeploymentPlan(acct *account, st *store.Store) error {
	rs, err := listSources(acct, st, TypeSageMakerEdgeDeploymentPlan)
	if err != nil {
		return err
	}
	fleetSet, err := scannedIDSet(acct, st, TypeSageMakerDeviceFleet)
	if err != nil {
		return err
	}
	for _, r := range rs {
		var a struct {
			DeviceFleetName *string `json:"DeviceFleetName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		name := sv(a.DeviceFleetName)
		if name == "" {
			continue
		}
		fleetID := store.ResourceID("aws", acct.ID, TypeSageMakerDeviceFleet,
			sagemakerARN(sv(r.Region), acct.ID, "device-fleet", name))
		if !fleetSet[fleetID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, fleetID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("edge-deployment-plan→device-fleet: %w", err)
		}
	}
	return nil
}
