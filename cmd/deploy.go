package cmd

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"text/template"

	"github.com/docker/docker/api/types"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/srl-wim/container-lab/clab"
)

// name of the container management network
var mgmtNetName string

// IPv4/6 address range for container management network
var mgmtIPv4Subnet net.IPNet
var mgmtIPv6Subnet net.IPNet

// reconfigure flag
var reconfigure bool

// deployCmd represents the deploy command
var deployCmd = &cobra.Command{
	Use:          "deploy",
	Short:        "deploy a lab",
	Aliases:      []string{"dep"},
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := []clab.ClabOption{
			clab.WithDebug(debug),
			clab.WithTimeout(timeout),
			clab.WithTopoFile(topo),
			clab.WithEnvDockerClient(),
		}
		c := clab.NewContainerLab(opts...)

		var err error
		setFlags(c.Config)
		log.Debugf("lab Conf: %+v", c.Config)
		// Parse topology information
		if err = c.ParseTopology(); err != nil {
			return err
		}
		if reconfigure {
			err = os.RemoveAll(c.Dir.Lab)
			if err != nil {
				return err
			}
		}
		if err = c.VerifyBridgesExist(); err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// create lab directory
		log.Info("Creating lab directory: ", c.Dir.Lab)
		clab.CreateDirectory(c.Dir.Lab, 0755)

		// create root CA
		tpl, err := template.ParseFiles(rootCaCsrTemplate)
		if err != nil {
			return fmt.Errorf("failed to parse rootCACsrTemplate: %v", err)
		}
		rootCerts, err := c.GenerateRootCa(tpl, clab.CaRootInput{Prefix: c.Config.Name})
		if err != nil {
			return fmt.Errorf("failed to generate rootCa: %v", err)
		}

		log.Debugf("root CSR: %s", string(rootCerts.Csr))
		log.Debugf("root Cert: %s", string(rootCerts.Cert))
		log.Debugf("root Key: %s", string(rootCerts.Key))

		// create bridge
		if err = c.CreateBridge(ctx); err != nil {
			log.Error(err)
		}

		certTpl, err := template.ParseFiles(certCsrTemplate)
		if err != nil {
			return fmt.Errorf("failed to parse certCsrTemplate: %v", err)
		}
		// create directory structure and container per node
		wg := new(sync.WaitGroup)
		wg.Add(len(c.Nodes))
		for _, node := range c.Nodes {
			go func(node *clab.Node) {
				defer wg.Done()
				if node.Kind == "bridge" {
					return
				}
				// create CERT
				nodeCerts, err := c.GenerateCert(
					path.Join(c.Dir.LabCARoot, "root-ca.pem"),
					path.Join(c.Dir.LabCARoot, "root-ca-key.pem"),
					certTpl,
					node,
				)
				if err != nil {
					log.Errorf("failed to generate certificates for node %s: %v", node.ShortName, err)
				}
				log.Debugf("%s CSR: %s", node.ShortName, string(nodeCerts.Csr))
				log.Debugf("%s Cert: %s", node.ShortName, string(nodeCerts.Cert))
				log.Debugf("%s Key: %s", node.ShortName, string(nodeCerts.Key))
				err = c.CreateNode(ctx, node, nodeCerts)
				if err != nil {
					log.Errorf("failed to create node %s: %v", node.ShortName, err)
				}
			}(node)
		}
		wg.Wait()
		// cleanup hanging resources if a deployment failed before
		log.Debug("cleaning up interfaces...")
		c.InitVirtualWiring()
		wg = new(sync.WaitGroup)
		wg.Add(len(c.Links))
		log.Debug("creating links...")
		// wire the links between the nodes based on cabling plan
		for _, link := range c.Links {
			go func(link *clab.Link) {
				defer wg.Done()
				if err = c.CreateVirtualWiring(link); err != nil {
					log.Error(err)
				}
			}(link)
		}
		wg.Wait()

		// generate graph of the lab topology
		if graph {
			if err = c.GenerateGraph(topo); err != nil {
				log.Error(err)
			}
		}
		log.Debug("containers created, retrieving state and IP addresses...")
		// show topology output

		labels = append(labels, "containerlab=lab-"+c.Config.Name)
		containers, err := c.ListContainers(ctx, labels)
		if err != nil {
			return fmt.Errorf("could not list containers: %v", err)
		}
		if len(containers) == 0 {
			return fmt.Errorf("no containers found")
		}

		log.Debug("enriching nodes with IP information...")
		enrichNodes(containers, c.Nodes, c.Config.Mgmt.Network)

		wg = new(sync.WaitGroup)
		wg.Add(len(c.Nodes))
		for _, node := range c.Nodes {
			go func(node *clab.Node) {
				defer wg.Done()
				err := c.ExecPostDeployTasks(ctx, node)
				if err != nil {
					log.Errorf("failed to run postdeploy task for node %s: %v", node.ShortName, err)
				}
			}(node)

		}
		wg.Wait()

		log.Info("Writing /etc/hosts file")
		err = createHostsFile(containers, c.Config.Mgmt.Network)
		if err != nil {
			log.Errorf("failed to create hosts file: %v", err)
		}
		// print table summary
		printContainerInspect(containers, c.Config.Mgmt.Network, format)
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

func createHostsFile(containers []types.Container, bridgeName string) error {
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
func hostsEntries(containers []types.Container, bridgeName string) []byte {
	buff := bytes.Buffer{}
	for _, cont := range containers {
		if len(cont.Names) == 0 {
			continue
		}
		if cont.NetworkSettings != nil {
			if br, ok := cont.NetworkSettings.Networks[bridgeName]; ok {
				if br.IPAddress != "" {
					buff.WriteString(br.IPAddress)
					buff.WriteString("\t")
					buff.WriteString(strings.TrimLeft(cont.Names[0], "/"))
					buff.WriteString("\n")
				}
				if br.GlobalIPv6Address != "" {
					buff.WriteString(br.GlobalIPv6Address)
					buff.WriteString("\t")
					buff.WriteString(strings.TrimLeft(cont.Names[0], "/"))
					buff.WriteString("\n")
				}
			}
		}
	}
	return buff.Bytes()
}

func enrichNodes(containers []types.Container, nodes map[string]*clab.Node, mgmtNet string) {
	for _, c := range containers {
		name = strings.Split(c.Names[0], "-")[2]
		if node, ok := nodes[name]; ok {
			// add network information
			node.MgmtNet = mgmtNet
			node.MgmtIPv4Address = c.NetworkSettings.Networks[mgmtNet].IPAddress
			node.MgmtIPv4PrefixLength = c.NetworkSettings.Networks[mgmtNet].IPPrefixLen
			node.MgmtIPv6Address = c.NetworkSettings.Networks[mgmtNet].GlobalIPv6Address
			node.MgmtIPv6PrefixLength = c.NetworkSettings.Networks[mgmtNet].GlobalIPv6PrefixLen

			node.ContainerID = c.ID
		}

	}
}
