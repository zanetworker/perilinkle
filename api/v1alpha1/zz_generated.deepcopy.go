package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func (in *ServiceGroup) DeepCopyInto(out *ServiceGroup) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *ServiceGroup) DeepCopy() *ServiceGroup {
	if in == nil {
		return nil
	}
	out := new(ServiceGroup)
	in.DeepCopyInto(out)
	return out
}

func (in *ServiceGroup) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *ServiceGroupList) DeepCopyInto(out *ServiceGroupList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ServiceGroup, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

func (in *ServiceGroupList) DeepCopy() *ServiceGroupList {
	if in == nil {
		return nil
	}
	out := new(ServiceGroupList)
	in.DeepCopyInto(out)
	return out
}

func (in *ServiceGroupList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *ServiceGroupSpec) DeepCopyInto(out *ServiceGroupSpec) {
	*out = *in
	in.ProviderProfile.DeepCopyInto(&out.ProviderProfile)
	if in.TargetServices != nil {
		out.TargetServices = make([]TargetService, len(in.TargetServices))
		for i := range in.TargetServices {
			in.TargetServices[i].DeepCopyInto(&out.TargetServices[i])
		}
	}
}

func (in *ProviderProfileSpec) DeepCopyInto(out *ProviderProfileSpec) {
	*out = *in
	in.EndpointDefaults.DeepCopyInto(&out.EndpointDefaults)
	if in.Binaries != nil {
		out.Binaries = make([]ProviderProfileBinary, len(in.Binaries))
		copy(out.Binaries, in.Binaries)
	}
}

func (in *ProviderProfileEndpointDefaults) DeepCopyInto(out *ProviderProfileEndpointDefaults) {
	*out = *in
	if in.AllowedIPs != nil {
		out.AllowedIPs = make([]string, len(in.AllowedIPs))
		copy(out.AllowedIPs, in.AllowedIPs)
	}
}

func (in *ProviderProfileBinary) DeepCopyInto(out *ProviderProfileBinary) {
	*out = *in
}

func (in *TargetService) DeepCopyInto(out *TargetService) {
	*out = *in
	in.Endpoint.DeepCopyInto(&out.Endpoint)
	in.Token.DeepCopyInto(&out.Token)
}

func (in *TargetServiceEndpoint) DeepCopyInto(out *TargetServiceEndpoint) {
	*out = *in
	if in.AllowedIPs != nil {
		out.AllowedIPs = make([]string, len(in.AllowedIPs))
		copy(out.AllowedIPs, in.AllowedIPs)
	}
}

func (in *TargetServiceToken) DeepCopyInto(out *TargetServiceToken) {
	*out = *in
	if in.Scopes != nil {
		out.Scopes = make([]string, len(in.Scopes))
		copy(out.Scopes, in.Scopes)
	}
}

func (in *ServiceGroupStatus) DeepCopyInto(out *ServiceGroupStatus) {
	*out = *in
	if in.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(in.Conditions))
		copy(out.Conditions, in.Conditions)
	}
}
