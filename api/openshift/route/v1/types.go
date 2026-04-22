/*
Copyright 2026.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: "route.openshift.io", Version: "v1"}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &Route{}, &RouteList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

type Route struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RouteSpec `json:"spec,omitempty"`
}

type RouteSpec struct {
	Host string               `json:"host,omitempty"`
	To   RouteTargetReference `json:"to"`
	Port *RoutePort           `json:"port,omitempty"`
}

type RouteTargetReference struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Weight *int32 `json:"weight,omitempty"`
}

type RoutePort struct {
	TargetPort intstr.IntOrString `json:"targetPort"`
}

func (r *Route) DeepCopyObject() runtime.Object {
	out := new(Route)
	out.TypeMeta = r.TypeMeta
	out.ObjectMeta = *r.ObjectMeta.DeepCopy()
	out.Spec = RouteSpec{
		Host: r.Spec.Host,
		To: RouteTargetReference{
			Kind: r.Spec.To.Kind,
			Name: r.Spec.To.Name,
		},
	}
	if r.Spec.To.Weight != nil {
		w := *r.Spec.To.Weight
		out.Spec.To.Weight = &w
	}
	if r.Spec.Port != nil {
		port := *r.Spec.Port
		out.Spec.Port = &port
	}
	return out
}

type RouteList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Route `json:"items"`
}

func (rl *RouteList) DeepCopyObject() runtime.Object {
	out := new(RouteList)
	out.TypeMeta = rl.TypeMeta
	out.ListMeta = rl.ListMeta
	if rl.Items != nil {
		out.Items = make([]Route, len(rl.Items))
		for i := range rl.Items {
			out.Items[i] = *rl.Items[i].DeepCopyObject().(*Route)
		}
	}
	return out
}
