/*
Copyright 2016 The Kubernetes Authors.

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

package discovery

import (
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"

	"k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
)

func TestNewDiscoveryValue(t *testing.T) {
	tests := []struct {
		d      *discoveryValue
		expect string
	}{
		{
			d: &discoveryValue{
				v: &kubeadm.Discovery{}},
			expect: "unknown",
		},
		{
			d: &discoveryValue{
				v: &kubeadm.Discovery{
					HTTPS: &kubeadm.HTTPSDiscovery{URL: "notnil"},
				},
			},
			expect: "notnil",
		},
		{
			d: &discoveryValue{
				v: &kubeadm.Discovery{
					File: &kubeadm.FileDiscovery{Path: "notnil"},
				},
			},
			expect: "file://notnil",
		},
		{
			d: &discoveryValue{
				v: &kubeadm.Discovery{
					Token: &kubeadm.TokenDiscovery{
						ID:        "foo",
						Secret:    "bar",
						Addresses: []string{"foobar"},
					},
				},
			}, expect: "token://foo.bar@foobar",
		},
	}
	for _, rt := range tests {
		actual := rt.d.String()
		if actual != rt.expect {
			t.Errorf(
				"failed discoveryValue string:\n\texpected: %s\n\t  actual: %s",
				rt.expect,
				actual,
			)
		}
	}
}

func TestType(t *testing.T) {
	tests := []struct {
		d      *discoveryValue
		expect string
	}{
		{d: &discoveryValue{}, expect: "discovery"},
	}
	for _, rt := range tests {
		actual := rt.d.Type()
		if actual != rt.expect {
			t.Errorf(
				"failed discoveryValue type:\n\texpected: %s\n\t  actual: %s",
				rt.expect,
				actual,
			)
		}
	}
}

func TestSet(t *testing.T) {
	tests := []struct {
		d      *discoveryValue
		s      string
		expect bool
	}{
		{d: &discoveryValue{v: &kubeadm.Discovery{}}, s: "", expect: false},
		{d: &discoveryValue{v: &kubeadm.Discovery{}}, s: "https://example.com", expect: true},
	}
	for _, rt := range tests {
		actual := rt.d.Set(rt.s)
		if (actual == nil) != rt.expect {
			t.Errorf(
				"failed discoveryValue set:\n\texpected: %t\n\t  actual: %t",
				rt.expect,
				(actual == nil),
			)
		}
	}
}

func TestParseURL(t *testing.T) {
	cases := []struct {
		url       string
		expect    kubeadm.Discovery
		expectErr bool
	}{
		{
			url: "token://",
			expect: kubeadm.Discovery{
				Token: &kubeadm.TokenDiscovery{},
			},
		},
		{
			url: "token://c05de9:ab224260fb3cd718",
			expect: kubeadm.Discovery{
				Token: &kubeadm.TokenDiscovery{
					ID:     "c05de9",
					Secret: "ab224260fb3cd718",
				},
			},
		},
		{
			url: "token://c05de9:ab224260fb3cd718@",
			expect: kubeadm.Discovery{
				Token: &kubeadm.TokenDiscovery{
					ID:     "c05de9",
					Secret: "ab224260fb3cd718",
				},
			},
		},
		{
			url: "token://c05de9:ab224260fb3cd718@192.168.0.1:6555,191.168.0.2:6443",
			expect: kubeadm.Discovery{
				Token: &kubeadm.TokenDiscovery{
					ID:     "c05de9",
					Secret: "ab224260fb3cd718",
					Addresses: []string{
						"192.168.0.1:6555",
						"191.168.0.2:6443",
					},
				},
			},
		},
		{
			url: "file:///foo/bar/baz",
			expect: kubeadm.Discovery{
				File: &kubeadm.FileDiscovery{
					Path: "/foo/bar/baz",
				},
			},
		},
		{
			url: "https://storage.googleapis.com/kubeadm-disco/clusters/217651295213",
			expect: kubeadm.Discovery{
				HTTPS: &kubeadm.HTTPSDiscovery{
					URL: "https://storage.googleapis.com/kubeadm-disco/clusters/217651295213",
				},
			},
		},
	}
	for _, c := range cases {
		var d kubeadm.Discovery
		if err := ParseURL(&d, c.url); err != nil {
			if !c.expectErr {
				t.Errorf("unexpected error parsing discovery url: %v", err)
			}
			continue
		}
		if !reflect.DeepEqual(d, c.expect) {
			t.Errorf("expected discovery config to be equal but got:\n\tactual: %s\n\texpected: %s", spew.Sdump(d), spew.Sdump(c.expect))
		}

	}
}
