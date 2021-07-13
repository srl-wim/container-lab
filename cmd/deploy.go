// Copyright 2020 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"

	cfssllog "github.com/cloudflare/cfssl/log"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/srl-labs/containerlab/cert"
	"github.com/srl-labs/containerlab/clab"
	"github.com/srl-labs/containerlab/nodes"
	"github.com/srl-labs/containerlab/runtime"
	"github.com/srl-labs/containerlab/types"
	"github.com/srl-labs/containerlab/utils"
)

// name of the container management network
var mgmtNetName string

// IPv4/6 address range for container management network
var mgmtIPv4Subnet net.IPNet
var mgmtIPv6Subnet net.IPNet

// reconfigure flag
var reconfigure bool

// max-workers flag
var maxWorkers uint

// deployCmd represents the deploy command
var deployCmd = &cobra.Command{
	Use:          "deploy",
	Short:        "deploy a lab",
	Long:         "deploy a lab based defined by means of the topology definition file\nreference: https://containerlab.srlinux.dev/cmd/deploy/",
	Aliases:      []string{"dep"},
	SilenceUsage: true,
	PreRunE:      sudoCheck,
	RunE: func(cmd *cobra.Command, args []string) error {
		var err error
		opts := []clab.ClabOption{
			clab.WithTimeout(timeout),
			clab.WithTopoFile(topo),
			clab.WithRuntime(rt,
				&runtime.RuntimeConfig{
					Debug:            debug,
					Timeout:          timeout,
					GracefulShutdown: graceful,
				},
			),
		}
		c, err := clab.NewContainerLab(opts...)
		if err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		setFlags(c.Config)
		log.Debugf("lab Conf: %+v", c.Config)

		// latest version channel
		vCh := make(chan string)
		go getLatestVersion(vCh)

		if reconfigure {
			if err != nil {
				return err
			}
			_ = destroyLab(ctx, c)
			log.Infof("Removing %s directory...", c.Dir.Lab)
			if err := os.RemoveAll(c.Dir.Lab); err != nil {
				return err
			}
		}

		if err = c.CheckTopologyDefinition(ctx); err != nil {
			return err
		}

		if err = c.CheckResources(); err != nil {
			return err
		}

		log.Info("Creating lab directory: ", c.Dir.Lab)
		utils.CreateDirectory(c.Dir.Lab, 0755)

		cfssllog.Level = cfssllog.LevelError
		if debug {
			cfssllog.Level = cfssllog.LevelDebug
		}
		if err := cert.CreateRootCA(c.Config.Name, c.Dir.LabCARoot, c.Nodes); err != nil {
			return err
		}

		// create docker network or use existing one
		if err = c.GlobalRuntime().CreateNet(ctx); err != nil {
			return err
		}

		nodeWorkers := uint(len(c.Nodes))
		linkWorkers := uint(len(c.Links))

		if maxWorkers > 0 && maxWorkers < nodeWorkers {
			nodeWorkers = maxWorkers
		}

		if maxWorkers > 0 && maxWorkers < linkWorkers {
			linkWorkers = maxWorkers
		}

		// a set of workers that do not support concurrency
		serialNodes := make(map[string]struct{})
		for _, n := range c.Nodes {
			if n.GetRuntime().GetName() == runtime.IgniteRuntime {
				serialNodes[n.Config().LongName] = struct{}{}
				nodeWorkers = nodeWorkers - 1
			}
		}

		c.CreateNodes(ctx, nodeWorkers, serialNodes)
		c.CreateLinks(ctx, linkWorkers, false)
		log.Debug("containers created, retrieving state and IP addresses...")

		// Building list of generic containers
		labels := []*types.GenericFilter{{FilterType: "label", Match: c.Config.Name, Field: "containerlab", Operator: "="}}
		containers, err := c.ListContainers(ctx, labels)
		if err != nil {
			return err
		}

		log.Debug("enriching nodes with IP information...")
		enrichNodes(containers, c.Nodes, c.Config.Mgmt.Network)

		if err := c.GenerateInventories(); err != nil {
			return err
		}

		wg := new(sync.WaitGroup)
		wg.Add(len(c.Nodes))

		for _, node := range c.Nodes {
			go func(node nodes.Node, wg *sync.WaitGroup) {
				defer wg.Done()
				err := node.PostDeploy(ctx, c.Nodes)
				if err != nil {
					log.Errorf("failed to run postdeploy task for node %s: %v", node.Config().ShortName, err)
				}
			}(node, wg)
		}
		wg.Wait()

		// Update containers after postDeploy action
		containers, err = c.ListContainers(ctx, labels)
		if err != nil {
			return err
		}

		// generate graph of the lab topology
		if graph {
			if err = c.GenerateGraph(topo); err != nil {
				log.Error(err)
			}
		}

		// run links postdeploy creation (ceos links creation)
		c.CreateLinks(ctx, linkWorkers, true)

		log.Info("Writing /etc/hosts file")
		err = createHostsFile(containers, c.Config.Mgmt.Network)
		if err != nil {
			log.Errorf("failed to create hosts file: %v", err)
		}

		// log new version availability info if ready
		newVerNotification(vCh)

		// print table summary
		printContainerInspect(c, containers, c.Config.Mgmt.Network, format)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(deployCmd)
	deployCmd.Flags().BoolVarP(&graph, "graph", "g", false, "generate topology graph")
	deployCmd.Flags().StringVarP(&mgmtNetName, "network", "", "", "management network name")
	deployCmd.Flags().IPNetVarP(&mgmtIPv4Subnet, "ipv4-subnet", "4", net.IPNet{}, "management network IPv4 subnet range")
	deployCmd.Flags().IPNetVarP(&mgmtIPv6Subnet, "ipv6-subnet", "6", net.IPNet{}, "management network IPv6 subnet range")
	deployCmd.Flags().BoolVarP(&reconfigure, "reconfigure", "", false, "regenerate configuration artifacts and overwrite the previous ones if any")
	deployCmd.Flags().UintVarP(&maxWorkers, "max-workers", "", 0, "limit the maximum number of workers creating nodes and virtual wires")
}

func setFlags(conf *clab.Config) {
	if name != "" {
		conf.Name = name
	}
	if mgmtNetName != "" {
		conf.Mgmt.Network = mgmtNetName
	}
	if mgmtIPv4Subnet.String() != "<nil>" {
		conf.Mgmt.IPv4Subnet = mgmtIPv4Subnet.String()
	}
	if mgmtIPv6Subnet.String() != "<nil>" {
		conf.Mgmt.IPv6Subnet = mgmtIPv6Subnet.String()
	}
}

func createHostsFile(containers []types.GenericContainer, bridgeName string) error {
	if bridgeName == "" {
		return fmt.Errorf("missing bridge name")
	}
	data := hostsEntries(containers, bridgeName)
	if len(data) == 0 {
		return nil
	}
	f, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, os.ModeAppend)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err != nil {
		return err
	}
	return nil
}

// hostEntries builds an /etc/hosts compliant text blob (as []byte]) for containers ipv4/6 address<->name pairs
func hostsEntries(containers []types.GenericContainer, bridgeName string) []byte {
	buff := bytes.Buffer{}
	for _, cont := range containers {
		if len(cont.Names) == 0 {
			continue
		}
		if cont.NetworkSettings.Set {
			if cont.NetworkSettings.IPv4addr != "" {
				buff.WriteString(cont.NetworkSettings.IPv4addr)
				buff.WriteString("\t")
				buff.WriteString(strings.TrimLeft(cont.Names[0], "/"))
				buff.WriteString("\n")
			}
			if cont.NetworkSettings.IPv6addr != "" {
				buff.WriteString(cont.NetworkSettings.IPv6addr)
				buff.WriteString("\t")
				buff.WriteString(strings.TrimLeft(cont.Names[0], "/"))
				buff.WriteString("\n")
			}
		}
	}
	return buff.Bytes()
}

func enrichNodes(containers []types.GenericContainer, nodesMap map[string]nodes.Node, mgmtNet string) {
	for _, c := range containers {
		name = c.Labels["clab-node-name"]
		if node, ok := nodesMap[name]; ok {
			// add network information
			// skipping host networking nodes as they don't have separate addresses
			if strings.ToLower(node.Config().NetworkMode) == "host" {
				continue
			}

			if c.NetworkSettings.Set {
				node.Config().MgmtIPv4Address = c.NetworkSettings.IPv4addr
				node.Config().MgmtIPv4PrefixLength = c.NetworkSettings.IPv4pLen
				node.Config().MgmtIPv6Address = c.NetworkSettings.IPv6addr
				node.Config().MgmtIPv6PrefixLength = c.NetworkSettings.IPv6pLen
			}

			node.Config().ContainerID = c.ID
		}
	}
}
