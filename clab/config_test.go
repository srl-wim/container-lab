package clab

import (
	"reflect"
	"strings"
	"testing"
)

func TestLicenseInit(t *testing.T) {
	tests := map[string]struct {
		got  string
		want string
	}{
		"node_license": {
			got:  "test_data/topo1.yml",
			want: "node1.lic",
		},
		"kind_license": {
			got:  "test_data/topo2.yml",
			want: "kind.lic",
		},
		"default_license": {
			got:  "test_data/topo3.yml",
			want: "default.lic",
		},
		"kind_overwrite": {
			got:  "test_data/topo4.yml",
			want: "node1.lic",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opts := []ClabOption{
				WithTopoFile(tc.got),
			}
			c := NewContainerLab(opts...)
			if err := c.ParseTopology(); err != nil {
				t.Fatal(err)
			}

			nodeCfg := c.Config.Topology.Nodes["node1"]
			node := Node{}
			node.Kind = strings.ToLower(c.kindInitialization(&nodeCfg))

			lic, err := c.licenseInit(&nodeCfg, &node)
			if err != nil {
				t.Fatal(err)
			}
			if lic != tc.want {
				t.Fatalf("wanted '%s' got '%s'", tc.want, lic)
			}
		})
	}
}

func TestBindsInit(t *testing.T) {
	tests := map[string]struct {
		got  string
		want []string
	}{
		"node_sing_bind": {
			got:  "test_data/topo1.yml",
			want: []string{"/node/src:/dst"},
		},
		"node_many_binds": {
			got:  "test_data/topo2.yml",
			want: []string{"/node/src1:/dst1", "/node/src2:/dst2"},
		},
		"kind_binds": {
			got:  "test_data/topo5.yml",
			want: []string{"/kind/src:/dst"},
		},
		"default_binds": {
			got:  "test_data/topo3.yml",
			want: []string{"/default/src:/dst"},
		},
		"node_binds_override": {
			got:  "test_data/topo4.yml",
			want: []string{"/node/src:/dst"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opts := []ClabOption{
				WithTopoFile(tc.got),
			}
			c := NewContainerLab(opts...)
			if err := c.ParseTopology(); err != nil {
				t.Fatal(err)
			}

			nodeCfg := c.Config.Topology.Nodes["node1"]
			node := Node{}
			node.Kind = strings.ToLower(c.kindInitialization(&nodeCfg))

			binds := c.bindsInit(&nodeCfg)
			if !reflect.DeepEqual(binds, tc.want) {
				t.Fatalf("wanted %q got %q", tc.want, binds)
			}
		})
	}
}

func TestTypeInit(t *testing.T) {
	tests := map[string]struct {
		got  string
		node string
		want string
	}{
		"undefined_type_returns_default": {
			got:  "test_data/topo1.yml",
			node: "node2",
			want: "ixr6",
		},
		"node_type_override_kind_type": {
			got:  "test_data/topo2.yml",
			node: "node2",
			want: "ixr10",
		},
		"node_inherits_kind_type": {
			got:  "test_data/topo2.yml",
			node: "node1",
			want: "ixrd2",
		},
		"node_inherits_default_type": {
			got:  "test_data/topo3.yml",
			node: "node2",
			want: "ixrd2",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opts := []ClabOption{
				WithTopoFile(tc.got),
			}
			c := NewContainerLab(opts...)
			if err := c.ParseTopology(); err != nil {
				t.Fatal(err)
			}

			nodeCfg := c.Config.Topology.Nodes[tc.node]
			node := Node{}
			node.Kind = strings.ToLower(c.kindInitialization(&nodeCfg))

			ntype := c.typeInit(&nodeCfg, node.Kind)
			if !reflect.DeepEqual(ntype, tc.want) {
				t.Fatalf("wanted %q got %q", tc.want, ntype)
			}
		})
	}
}

func TestEnvInit(t *testing.T) {
	tests := map[string]struct {
		got  string
		node string
		want map[string]string
	}{
		"env_defined_at_node_level": {
			got:  "test_data/topo1.yml",
			node: "node1",
			want: map[string]string{
				"env1": "val1",
				"env2": "val2",
			},
		},
		"env_defined_at_kind_level": {
			got:  "test_data/topo2.yml",
			node: "node2",
			want: map[string]string{
				"env1": "val1",
			},
		},
		"env_defined_at_defaults_level": {
			got:  "test_data/topo3.yml",
			node: "node1",
			want: map[string]string{
				"env1": "val1",
			},
		},
		"env_defined_at_node_and_kind_and_default_level": {
			got:  "test_data/topo4.yml",
			node: "node1",
			want: map[string]string{
				"env1": "node",
				"env2": "kind",
				"env3": "global",
				"env4": "kind",
				"env5": "node",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opts := []ClabOption{
				WithTopoFile(tc.got),
			}
			c := NewContainerLab(opts...)
			if err := c.ParseTopology(); err != nil {
				t.Fatal(err)
			}

			nodeCfg := c.Config.Topology.Nodes[tc.node]
			kind := strings.ToLower(c.kindInitialization(&nodeCfg))
			env := c.envInit(&nodeCfg, kind)
			if !reflect.DeepEqual(env, tc.want) {
				t.Fatalf("wanted %q got %q", tc.want, env)
			}
		})
	}
}

func TestUserInit(t *testing.T) {
	tests := map[string]struct {
		got  string
		node string
		want string
	}{
		"user_defined_at_node_level": {
			got:  "test_data/topo1.yml",
			node: "node2",
			want: "custom",
		},
		"user_defined_at_kind_level": {
			got:  "test_data/topo2.yml",
			node: "node2",
			want: "customkind",
		},
		"user_defined_at_defaults_level": {
			got:  "test_data/topo3.yml",
			node: "node1",
			want: "customglobal",
		},
		"user_defined_at_node_and_kind_and_default_level": {
			got:  "test_data/topo4.yml",
			node: "node1",
			want: "customnode",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opts := []ClabOption{
				WithTopoFile(tc.got),
			}
			c := NewContainerLab(opts...)
			if err := c.ParseTopology(); err != nil {
				t.Fatal(err)
			}

			nodeCfg := c.Config.Topology.Nodes[tc.node]
			kind := strings.ToLower(c.kindInitialization(&nodeCfg))
			user := c.userInit(&nodeCfg, kind)
			if user != tc.want {
				t.Fatalf("wanted %q got %q", tc.want, user)
			}
		})
	}
}