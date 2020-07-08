// Copyright (c) 2020 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package starlark

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/vmware-tanzu/crash-diagnostics/k8s"
	coreV1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

// KubeNodesProviderFn is a built-in starlark function that collects compute resources from a k8s cluster
// Starlark format: kube_nodes_provider([kube_config=kube_config(), ssh_config=ssh_config(), names=["foo", "bar], labels=["bar", "baz"]])
func KubeNodesProviderFn(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {

	structVal, err := kwargsToStruct(kwargs)
	if err != nil {
		return starlark.None, err
	}

	return newKubeNodesProvider(thread, structVal)
}

// newKubeNodesProvider returns a struct with k8s cluster node provider info
func newKubeNodesProvider(thread *starlark.Thread, structVal *starlarkstruct.Struct) (*starlarkstruct.Struct, error) {
	kubeconfig, err := getKubeConfigPath(thread, structVal)
	if err != nil {
		return nil, errors.Wrap(err, "failed to kubeconfig")
	}
	client, err := k8s.New(kubeconfig)
	if err != nil {
		return nil, errors.Wrap(err, "could not initialize search client")
	}

	searchParams := generateSearchParams(structVal)
	nodes, err := getNodes(client, searchParams.Names(), searchParams.Labels())
	if err != nil {
		return nil, errors.Wrapf(err, "could not fetch nodes")
	}

	// dictionary for node provider struct
	kubeNodesProviderDict := starlark.StringDict{
		"kind":      starlark.String(identifiers.kubeNodesProvider),
		"transport": starlark.String("ssh"),
	}

	// add node info to dictionary
	var nodeIps []starlark.Value
	for _, node := range nodes {
		nodeIps = append(nodeIps, starlark.String(getNodeInternalIP(node)))
	}
	kubeNodesProviderDict["hosts"] = starlark.NewList(nodeIps)

	// add ssh info to dictionary
	if _, ok := kubeNodesProviderDict[identifiers.sshCfg]; !ok {
		data := thread.Local(identifiers.sshCfg)
		sshcfg, ok := data.(*starlarkstruct.Struct)
		if !ok {
			return nil, fmt.Errorf("%s: default ssh_config not found", identifiers.kubeNodesProvider)
		}
		kubeNodesProviderDict[identifiers.sshCfg] = sshcfg
	}

	return starlarkstruct.FromStringDict(starlarkstruct.Default, kubeNodesProviderDict), nil
}

func generateSearchParams(structVal *starlarkstruct.Struct) k8s.SearchParams {
	// change nodes key to names
	if _, err := structVal.Attr("nodes"); err == nil {
		dict := starlark.StringDict{}
		structVal.ToStringDict(dict)

		dict["names"] = dict["nodes"]
		structVal = starlarkstruct.FromStringDict(starlarkstruct.Default, dict)
	}
	return k8s.NewSearchParams(structVal)
}

func getNodes(k8sc *k8s.Client, names, labels string) ([]*coreV1.Node, error) {
	nodeResults, err := k8sc.Search(
		"core",  // group
		"nodes", // kind
		"",      // namespaces
		"",      // version
		names,
		labels,
		"", // containers
	)
	if err != nil {
		return nil, err
	}

	// collate
	var nodes []*coreV1.Node
	for _, result := range nodeResults {
		for _, item := range result.List.Items {
			node := new(coreV1.Node)
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(item.Object, &node); err != nil {
				return nil, err
			}
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}

func getNodeInternalIP(node *coreV1.Node) (ipAddr string) {
	for _, addr := range node.Status.Addresses {
		if addr.Type == "InternalIP" {
			ipAddr = addr.Address
			return
		}
	}
	return
}