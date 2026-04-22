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
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: "serving.knative.dev", Version: "v1"}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion, &Service{}, &ServiceList{})
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

type Service struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            ServiceStatus `json:"status,omitempty"`
}

func (s *Service) IsReady() bool {
	for _, c := range s.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == "True"
		}
	}

	return false
}

type ServiceStatus struct {
	URL        string      `json:"url,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

type Condition struct {
	Type   string `json:"type"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

func (s *Service) DeepCopyObject() runtime.Object {
	out := new(Service)
	*out = *s
	out.ObjectMeta = *s.ObjectMeta.DeepCopy()
	if s.Status.Conditions != nil {
		out.Status.Conditions = make([]Condition, len(s.Status.Conditions))
		copy(out.Status.Conditions, s.Status.Conditions)
	}
	return out
}

type ServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Service `json:"items"`
}

func (sl *ServiceList) DeepCopyObject() runtime.Object {
	out := new(ServiceList)
	out.TypeMeta = sl.TypeMeta
	out.ListMeta = sl.ListMeta
	if sl.Items != nil {
		out.Items = make([]Service, len(sl.Items))
		for i := range sl.Items {
			out.Items[i] = *sl.Items[i].DeepCopyObject().(*Service)
		}
	}
	return out
}
