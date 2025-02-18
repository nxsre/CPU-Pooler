module github.com/nokia/CPU-Pooler

go 1.23

toolchain go1.23.1

require (
	github.com/fasthttp/router v1.5.2
	github.com/fsnotify/fsnotify v1.7.0
	github.com/go-resty/resty/v2 v2.14.0
	github.com/golang/glog v1.2.2
	github.com/json-iterator/go v1.1.12
	github.com/nxsre/toolkit v0.0.0-20240908133829-71ff0ecf8bb5
	github.com/prometheus/client_model v0.6.1
	github.com/prometheus/common v0.55.0
	github.com/stretchr/testify v1.9.0
	github.com/valyala/fasthttp v1.55.0
	golang.org/x/net v0.33.0
	golang.org/x/sys v0.28.0
	google.golang.org/grpc v1.67.1
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.31.1
	k8s.io/apimachinery v0.31.1
	k8s.io/client-go v0.31.1
	k8s.io/kubelet v0.28.8
	k8s.io/kubernetes v1.28.8
	k8s.io/utils v0.0.0-20240711033017-18e509b52bc8
)

require (
	github.com/andybalholm/brotli v1.1.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dswarbrick/go-nvme v0.0.0-20221105204844-b56b9dfdcd90 // indirect
	github.com/emicklei/go-restful/v3 v3.11.0 // indirect
	github.com/evanphx/json-patch v5.6.0+incompatible // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-openapi/jsonpointer v0.20.2 // indirect
	github.com/go-openapi/jsonreference v0.20.4 // indirect
	github.com/go-openapi/swag v0.22.9 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/jaypipes/pcidb v1.0.1 // indirect
	github.com/jkeiser/iter v0.0.0-20200628201005-c8aa0ae784d1 // indirect
	github.com/jochenvg/go-udev v0.0.0-20240801134859-b65ed646224b // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mitchellh/go-homedir v1.1.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/savsgio/gotils v0.0.0-20240704082632-aef3928b8a38 // indirect
	github.com/smallnest/weighted v0.0.0-20230419055410-36b780e40a7a // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/u-root/u-root v0.14.0 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	golang.org/x/exp v0.0.0-20241210194714-1829a127f884 // indirect
	golang.org/x/oauth2 v0.23.0 // indirect
	golang.org/x/term v0.27.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	golang.org/x/time v0.8.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241007155032-5fefd90f89a9 // indirect
	google.golang.org/protobuf v1.35.1 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20240228011516-70dd3763d340 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
	sigs.k8s.io/yaml v1.4.0 // indirect
)

replace k8s.io/api => k8s.io/api v0.28.8

replace k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.28.8

replace k8s.io/apimachinery => k8s.io/apimachinery v0.28.8

replace k8s.io/apiserver => k8s.io/apiserver v0.28.8

replace k8s.io/cli-runtime => k8s.io/cli-runtime v0.28.8

replace k8s.io/client-go => k8s.io/client-go v0.28.8

replace k8s.io/cloud-provider => k8s.io/cloud-provider v0.28.8

replace k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.28.8

replace k8s.io/code-generator => k8s.io/code-generator v0.28.8

replace k8s.io/component-base => k8s.io/component-base v0.28.8

replace k8s.io/component-helpers => k8s.io/component-helpers v0.28.8

replace k8s.io/controller-manager => k8s.io/controller-manager v0.28.8

replace k8s.io/cri-api => k8s.io/cri-api v0.28.8

replace k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.28.8

replace k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.28.8

replace k8s.io/endpointslice => k8s.io/endpointslice v0.28.8

replace k8s.io/kms => k8s.io/kms v0.28.8

replace k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.28.8

replace k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.28.8

replace k8s.io/kube-proxy => k8s.io/kube-proxy v0.28.8

replace k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.28.8

replace k8s.io/kubectl => k8s.io/kubectl v0.28.8

replace k8s.io/kubelet => k8s.io/kubelet v0.28.8

replace k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.28.8

replace k8s.io/metrics => k8s.io/metrics v0.28.8

replace k8s.io/mount-utils => k8s.io/mount-utils v0.28.8

replace k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.28.8

replace k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.28.8

replace k8s.io/sample-cli-plugin => k8s.io/sample-cli-plugin v0.28.8

replace k8s.io/sample-controller => k8s.io/sample-controller v0.28.8

replace github.com/nxsre/toolkit => /opt/workspaces/cph/toolkit
