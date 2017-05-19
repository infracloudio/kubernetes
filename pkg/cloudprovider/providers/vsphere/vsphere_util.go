/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vsphere

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/golang/glog"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/pbm"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"

	pbmtypes "github.com/vmware/govmomi/pbm/types"
)

const (
	DatastoreProperty      = "datastore"
	DatastoreInfoProperty  = "info"
	ParentProperty         = "parent"
	ClusterComputeResource = "ClusterComputeResource"
	ComputeResource        = "ComputeResource"
)

// Reads vSphere configuration from system environment and construct vSphere object
func GetVSphere() (*VSphere, error) {
	cfg := getVSphereConfig()
	client, err := GetgovmomiClient(cfg)
	if err != nil {
		return nil, err
	}
	vs := &VSphere{
		client:          client,
		cfg:             cfg,
		localInstanceID: "",
	}
	runtime.SetFinalizer(vs, logout)
	return vs, nil
}

func getVSphereConfig() *VSphereConfig {
	var cfg VSphereConfig
	cfg.Global.VCenterIP = os.Getenv("VSPHERE_VCENTER")
	cfg.Global.User = os.Getenv("VSPHERE_USER")
	cfg.Global.Password = os.Getenv("VSPHERE_PASSWORD")
	cfg.Global.Datacenter = os.Getenv("VSPHERE_DATACENTER")
	cfg.Global.Datastore = os.Getenv("VSPHERE_DATASTORE")
	cfg.Global.WorkingDir = os.Getenv("VSPHERE_WORKING_DIR")
	cfg.Global.VMName = os.Getenv("VSPHERE_VM_NAME")
	cfg.Global.InsecureFlag = false
	if strings.ToLower(os.Getenv("VSPHERE_INSECURE")) == "true" {
		cfg.Global.InsecureFlag = true
	}
	return &cfg
}

func GetgovmomiClient(cfg *VSphereConfig) (*govmomi.Client, error) {
	if cfg == nil {
		cfg = getVSphereConfig()
	}
	client, err := newClient(context.TODO(), cfg)
	return client, err
}

// Get placement compatibility result based on storage policy requirements.
func (vs *VSphere) GetPlacementCompatibilityResult(ctx context.Context, pbmClient *pbm.Client, resourcePool *object.ResourcePool, storagePolicyID string) (pbm.PlacementCompatibilityResult, error) {
	datastores, err := vs.getAllAccessibleDatastores(ctx, resourcePool)
	if err != nil {
		return nil, err
	}
	var hubs []pbmtypes.PbmPlacementHub
	for _, ds := range datastores {
		hubs = append(hubs, pbmtypes.PbmPlacementHub{
			HubType: ds.Type,
			HubId:   ds.Value,
		})
	}
	req := []pbmtypes.BasePbmPlacementRequirement{
		&pbmtypes.PbmPlacementCapabilityProfileRequirement{
			ProfileId: pbmtypes.PbmProfileId{
				UniqueId: storagePolicyID,
			},
		},
	}
	res, err := pbmClient.CheckRequirements(ctx, hubs, nil, req)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Verify if the user specified datastore is in the list of non-compatible datastores.
// If yes, return the non compatible datastore reference.
func (vs *VSphere) IsUserSpecifiedDatastoreNonCompatible(ctx context.Context, compatibilityResult pbm.PlacementCompatibilityResult, dsName string) (bool, *types.ManagedObjectReference) {
	dsMoList := vs.GetNonCompatibleDatastoresMo(ctx, compatibilityResult)
	for _, ds := range dsMoList {
		if ds.Info.GetDatastoreInfo().Name == dsName {
			dsMoRef := ds.Reference()
			return true, &dsMoRef
		}
	}
	return false, nil
}

func GetNonCompatibleDatastoreFaultMsg(compatibilityResult pbm.PlacementCompatibilityResult, dsMoref types.ManagedObjectReference) string {
	var faultMsg string
	for _, res := range compatibilityResult {
		if res.Hub.HubId == dsMoref.Value {
			for _, err := range res.Error {
				faultMsg = faultMsg + err.LocalizedMessage
			}
		}
	}
	return faultMsg
}

// Get the best fit compatible datastore by free space.
func GetMostFreeDatastore(dsMo []mo.Datastore) mo.Datastore {
	var curMax int64
	curMax = -1
	var index int
	for i, ds := range dsMo {
		dsFreeSpace := ds.Info.GetDatastoreInfo().FreeSpace
		if dsFreeSpace > curMax {
			curMax = dsFreeSpace
			index = i
		}
	}
	return dsMo[index]
}

func (vs *VSphere) GetCompatibleDatastoresMo(ctx context.Context, compatibilityResult pbm.PlacementCompatibilityResult) []mo.Datastore {
	compatibleHubs := compatibilityResult.CompatibleDatastores()
	// Return an error if there are no compatible datastores.
	if len(compatibleHubs) < 1 {
		return nil
	}
	dsMoList, err := vs.getDatastoreMo(ctx, compatibleHubs)
	if err != nil {
		return nil
	}
	return dsMoList
}

func (vs *VSphere) GetNonCompatibleDatastoresMo(ctx context.Context, compatibilityResult pbm.PlacementCompatibilityResult) []mo.Datastore {
	nonCompatibleHubs := compatibilityResult.NonCompatibleDatastores()
	// Return an error if there are no compatible datastores.
	if len(nonCompatibleHubs) < 1 {
		return nil
	}
	dsMoList, err := vs.getDatastoreMo(ctx, nonCompatibleHubs)
	if err != nil {
		return nil
	}
	return dsMoList
}

// Get the datastore managed objects for the place hubs using property collector.
func (vs *VSphere) getDatastoreMo(ctx context.Context, hubs []pbmtypes.PbmPlacementHub) ([]mo.Datastore, error) {
	var dsMoRefs []types.ManagedObjectReference
	for _, hub := range hubs {
		dsMoRefs = append(dsMoRefs, types.ManagedObjectReference{
			Type:  hub.HubType,
			Value: hub.HubId,
		})
	}

	pc := property.DefaultCollector(vs.client.Client)
	var dsMoList []mo.Datastore
	err := pc.Retrieve(ctx, dsMoRefs, []string{DatastoreInfoProperty}, &dsMoList)
	if err != nil {
		return nil, err
	}
	return dsMoList, nil
}

// Get all datastores accessible for the virtual machine object.
func (vs *VSphere) getAllAccessibleDatastores(ctx context.Context, resourcePool *object.ResourcePool) ([]types.ManagedObjectReference, error) {
	var datastores []types.ManagedObjectReference
	resourcePoolMo, err := vs.getParentResourcePoolMo(ctx, resourcePool)
	if err != nil {
		return nil, err
	}
	s := object.NewSearchIndex(vs.client.Client)
	switch resourcePoolMo.Parent.Type {
	case ClusterComputeResource:
		var cluster mo.ClusterComputeResource
		err := s.Properties(ctx, resourcePoolMo.Parent.Reference(), []string{DatastoreProperty}, &cluster)
		if err != nil {
			return nil, err
		}
		hostMoList, err := vs.getHostsMo(ctx, cluster.Host)
		if err != nil {
			return nil, err
		}
		datastores, err = vs.getSharedDatastoresFromHosts(ctx, hostMoList)
		if err != nil {
			return nil, err
		}
	case ComputeResource:
		var host mo.ComputeResource
		err := s.Properties(ctx, resourcePoolMo.Parent.Reference(), []string{DatastoreProperty}, &host)
		if err != nil {
			return nil, err
		}
		datastores = host.Datastore
	default:
		return nil, fmt.Errorf("Failed to get datastores - unknown parent type: %s", resourcePoolMo.Parent.Type)
	}
	return datastores, nil
}

func (vs *VSphere) getSharedDatastoresFromHosts(ctx context.Context, hostMoList []mo.HostSystem) ([]types.ManagedObjectReference, error) {
	dsMorefs := make(map[int][]types.ManagedObjectReference)
	for i, hostMo := range hostMoList {
		dsMorefs[i] = hostMo.Datastore
		glog.V(1).Infof("balu - [getSharedDatastoresFromHosts] i: %d and dsMorefs: %+v", i, dsMorefs[i])
	}
	glog.V(1).Infof("balu - [getSharedDatastoresFromHosts] dsMorefs: %+v", dsMorefs)
	sharedDSMorefs := intersectMorefs(dsMorefs)
	glog.V(1).Infof("balu - [getSharedDatastoresFromHosts] intersectMorefs: %+v", sharedDSMorefs)
	if len(sharedDSMorefs) == 0 {
		glog.V(1).Infof("balu - [getSharedDatastoresFromHosts] No shared datastores found in the Kubernetes cluster")
		return nil, fmt.Errorf("No shared datastores found in the Kubernetes cluster")
	}
	return sharedDSMorefs, nil
}

func intersectMorefs(args map[int][]types.ManagedObjectReference) []types.ManagedObjectReference {
	arrLength := len(args)
	tempMap := make(map[string]int)
	tempArrayNew := make([]types.ManagedObjectReference, 0)
	for _, arg := range args {
		tempArr := arg
		glog.V(1).Infof("balu - [intersectMorefs] tempArr starting: %+v", tempArr)
		for idx := range tempArr {
			if _, ok := tempMap[tempArr[idx].Value]; ok {
				tempMap[tempArr[idx].Value]++
				if tempMap[tempArr[idx].Value] == arrLength {
					glog.V(1).Infof("balu - [intersectMorefs] tempArrayNew before: %+v", tempArrayNew)
					glog.V(1).Infof("balu - [intersectMorefs] arg[idx]: %+v", arg[idx])
					tempArrayNew = append(tempArrayNew, arg[idx])
					glog.V(1).Infof("balu - [intersectMorefs] tempArrayNew after: %+v", tempArrayNew)
				}
			} else {
				tempMap[tempArr[idx].Value] = 1
			}
		}
	}
	return tempArrayNew
}

func (vs *VSphere) getHostsMo(ctx context.Context, refs []types.ManagedObjectReference) ([]mo.HostSystem, error) {
	var hostMoList []mo.HostSystem
	pc := property.DefaultCollector(vs.client.Client)
	err := pc.Retrieve(ctx, refs, []string{DatastoreProperty}, &hostMoList)
	if err != nil {
		return nil, err
	}
	return hostMoList, nil
}

func (vs *VSphere) getParentResourcePoolMo(ctx context.Context, resourcePool *object.ResourcePool) (*mo.ResourcePool, error) {
	var resourcePoolMo mo.ResourcePool
	var resourcePoolType string
	s := object.NewSearchIndex(vs.client.Client)
	for {
		err := s.Properties(ctx, resourcePool.Reference(), []string{ParentProperty}, &resourcePoolMo)
		if err != nil {
			return nil, err
		}
		resourcePoolType = resourcePoolMo.Parent.Type
		if resourcePoolType == ClusterComputeResource || resourcePoolType == ComputeResource {
			break
		}
	}
	return &resourcePoolMo, nil
}
