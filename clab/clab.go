// Copyright 2020 Nokia
// Licensed under the BSD 3-Clause License.
// SPDX-License-Identifier: BSD-3-Clause

package clab

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/srl-labs/containerlab/nodes"
	_ "github.com/srl-labs/containerlab/nodes/all"
	"github.com/srl-labs/containerlab/runtime"
	_ "github.com/srl-labs/containerlab/runtime/all"
	"github.com/srl-labs/containerlab/types"
	"github.com/srl-labs/containerlab/utils"
)

type CLab struct {
	Config        *Config
	TopoFile      *TopoFile
	m             *sync.RWMutex
	Nodes         map[string]nodes.Node
	Links         map[int]*types.Link
	Runtimes      map[string]runtime.ContainerRuntime
	globalRuntime string
	Dir           *Directory

	timeout time.Duration
}

type Directory struct {
	Lab       string
	LabCA     string
	LabCARoot string
	LabGraph  string
}

type ClabOption func(c *CLab)

func WithTimeout(dur time.Duration) ClabOption {
	return func(c *CLab) {
		c.timeout = dur
	}
}

func WithRuntime(name string, rtconfig *runtime.RuntimeConfig) ClabOption {
	return func(c *CLab) {
		// define runtime name.
		// order of preference: cli flag -> env var -> default value of docker
		envN := os.Getenv("CLAB_RUNTIME")
		switch {
		case name != "":
		case envN != "":
			name = envN
		default:
			name = runtime.DockerRuntime
		}
		c.globalRuntime = name

		if rInit, ok := runtime.ContainerRuntimes[name]; ok {
			r := rInit()
			err := r.Init(
				runtime.WithConfig(rtconfig),
				runtime.WithMgmtNet(c.Config.Mgmt),
			)
			if err != nil {
				log.Fatalf("failed to init the container runtime: %s", err)
			}

			c.Runtimes[name] = r

			return
		}
		log.Fatalf("unknown container runtime %q", name)
	}
}

func WithKeepMgmtNet() ClabOption {
	return func(c *CLab) {
		c.GlobalRuntime().WithKeepMgmtNet()
	}
}

func WithTopoFile(file string) ClabOption {
	return func(c *CLab) {
		if file == "" {
			log.Fatal("provide a path to the clab topology file")
		}
		if err := c.GetTopology(file); err != nil {
			log.Fatalf("failed to read topology file: %v", err)
		}

		err := c.initMgmtNetwork()
		if err != nil {
			log.Fatalf("failed to init the management network: %s", err)
		}
	}
}

// NewContainerLab function defines a new container lab
func NewContainerLab(opts ...ClabOption) (*CLab, error) {
	c := &CLab{
		Config: &Config{
			Mgmt:     new(types.MgmtNet),
			Topology: types.NewTopology(),
		},
		TopoFile: new(TopoFile),
		m:        new(sync.RWMutex),
		Nodes:    make(map[string]nodes.Node),
		Links:    make(map[int]*types.Link),
		Runtimes: make(map[string]runtime.ContainerRuntime),
	}

	for _, o := range opts {
		o(c)
	}

	err := c.parseTopology()

	return c, err
}

// initMgmtNetwork sets management network config
func (c *CLab) initMgmtNetwork() error {
	if c.Config.Mgmt.Network == "" {
		c.Config.Mgmt.Network = dockerNetName
	}
	if c.Config.Mgmt.IPv4Subnet == "" && c.Config.Mgmt.IPv6Subnet == "" {
		c.Config.Mgmt.IPv4Subnet = dockerNetIPv4Addr
		c.Config.Mgmt.IPv6Subnet = dockerNetIPv6Addr
	}

	// init docker network mtu
	if c.Config.Mgmt.MTU == "" {
		m, err := utils.DefaultNetMTU()
		if err != nil {
			log.Warnf("Error occurred during getting the default docker MTU: %v", err)
		}
		c.Config.Mgmt.MTU = m
	}

	return nil
}

func (c *CLab) GlobalRuntime() runtime.ContainerRuntime {
	return c.Runtimes[c.globalRuntime]
}

func (c *CLab) CreateNodes(ctx context.Context, maxWorkers uint, serialNodes map[string]struct{}) {

	wg := new(sync.WaitGroup)
	if len(serialNodes) > 0 {
		wg.Add(int(maxWorkers) + 1)
	} else {
		wg.Add(int(maxWorkers))
	}

	concurrentChan := make(chan nodes.Node)
	serialChan := make(chan nodes.Node)

	workerFunc := func(i uint, input chan nodes.Node, wg *sync.WaitGroup) {
		defer wg.Done()
		for {
			select {
			case node, ok := <-input:
				if node == nil || !ok {
					log.Debugf("Worker %d terminating...", i)
					return
				}
				log.Debugf("Worker %d received node: %+v", i, node.Config())
				// PreDeploy
				err := node.PreDeploy(c.Config.Name, c.Dir.LabCA, c.Dir.LabCARoot)
				if err != nil {
					log.Errorf("failed pre-deploy phase for node %q: %v", node.Config().ShortName, err)
					continue
				}
				// Deploy
				err = node.Deploy(ctx)
				if err != nil {
					log.Errorf("failed deploy phase for node %q: %v", node.Config().ShortName, err)
					continue
				}
			case <-ctx.Done():
				return
			}
		}
	}

	// start concurrent workers
	for i := uint(0); i < maxWorkers; i++ {
		go workerFunc(i, concurrentChan, wg)
	}

	// start the serial worker
	go workerFunc(maxWorkers, serialChan, wg)

	// send nodes to workers
	for _, n := range c.Nodes {
		if _, ok := serialNodes[n.Config().LongName]; ok {
			serialChan <- n
			continue
		}
		concurrentChan <- n
	}

	// close channel to terminate the workers
	close(concurrentChan)
	close(serialChan)

	// wait for all workers to finish
	wg.Wait()
}

// CreateLinks creates links using the specified number of workers
// `postdeploy` indicates the stage of links creation.
// `postdeploy=true` means the links routine is called after nodes postdeploy tasks
func (c *CLab) CreateLinks(ctx context.Context, workers uint, postdeploy bool) {
	wg := new(sync.WaitGroup)
	wg.Add(int(workers))
	linksChan := make(chan *types.Link)

	log.Debug("creating links...")
	// wire the links between the nodes based on cabling plan
	for i := uint(0); i < workers; i++ {
		go func(i uint) {
			defer wg.Done()
			for {
				select {
				case link := <-linksChan:
					if link == nil {
						log.Debugf("Link worker %d terminating...", i)
						return
					}
					log.Debugf("Link worker %d received link: %+v", i, link)
					if err := c.CreateVirtualWiring(link); err != nil {
						log.Error(err)
					}
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}

	for _, link := range c.Links {
		// skip the links of ceos kind
		// ceos containers need to be restarted in the postdeploy stage, thus their data links
		// will get recreated after post-deploy stage
		if !postdeploy {
			if link.A.Node.Kind == "ceos" || link.B.Node.Kind == "ceos" {
				continue
			}
			linksChan <- link
		} else {
			// postdeploy stage
			// create ceos links that were skipped during original links creation
			if link.A.Node.Kind == "ceos" || link.B.Node.Kind == "ceos" {
				linksChan <- link
			}
		}
	}
	// close channel to terminate the workers
	close(linksChan)
	// wait for all workers to finish
	wg.Wait()
}

func (c *CLab) DeleteNodes(ctx context.Context, workers uint, deleteCandidates map[string]nodes.Node) {
	wg := new(sync.WaitGroup)

	nodeChan := make(chan nodes.Node)
	wg.Add(int(workers))
	for i := uint(0); i < workers; i++ {
		go func(i uint) {
			defer wg.Done()
			for {
				select {
				case n := <-nodeChan:
					if n == nil {
						log.Debugf("Worker %d terminating...", i)
						return
					}
					err := n.Delete(ctx)
					if err != nil {
						log.Errorf("could not remove container %q: %v", n.Config().LongName, err)
					}
				case <-ctx.Done():
					return
				}
			}
		}(i)
	}
	for _, n := range deleteCandidates {
		nodeChan <- n
	}
	close(nodeChan)

	wg.Wait()

}

func (c *CLab) ListContainers(ctx context.Context, labels []*types.GenericFilter) ([]types.GenericContainer, error) {
	var containers []types.GenericContainer

	for _, r := range c.Runtimes {
		ctrs, err := r.ListContainers(ctx, labels)
		if err != nil {
			return containers, fmt.Errorf("could not list containers: %v", err)
		}
		containers = append(containers, ctrs...)
	}
	return containers, nil
}

func (c *CLab) GetNodeRuntime(query string) (runtime.ContainerRuntime, error) {
	shortName, err := getShortName(c.Config.Name, query)
	if err != nil {
		return nil, err
	}

	if node, ok := c.Nodes[shortName]; ok {
		return node.GetRuntime(), nil
	}

	return nil, fmt.Errorf("could not find a container matching name %q", query)
}
