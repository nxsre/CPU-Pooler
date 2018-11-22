package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"github.com/golang/glog"
	"github.com/nokia/CPU-Pooler/internal/types"
	"io/ioutil"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"net/http"
	"strconv"
	"time"
)

var scheme = runtime.NewScheme()
var codecs = serializer.NewCodecFactory(scheme)
var poolConf types.PoolConfig

type patch struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value"`
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func toAdmissionResponse(err error) *v1beta1.AdmissionResponse {
	return &v1beta1.AdmissionResponse{
		Result: &metav1.Status{
			Message: err.Error(),
		},
	}
}

func isContainerPatchNeeded(containerName string, containersToPatch []string) bool {
	for _, name := range containersToPatch {
		if name == containerName {
			return true
		}
	}
	return false
}

func annotationNameFromConfig() string {
	return poolConf.ResourceBaseName + "/cpus"

}

func cpuPoolResourcePatch(cpuAnnotation types.CPUAnnotation, pool string, containerIndex int, cName string) (patchItem patch) {
	cpuReq := cpuAnnotation.ContainerTotalCPURequest(pool, cName)
	cpuVal := `"` + strconv.Itoa(cpuReq) + `"`
	patchItem.Op = "add"
	patchItem.Path = "/spec/containers/" + strconv.Itoa(containerIndex) + "/resources/limits/" + poolConf.ResourceBaseName + "~1" + pool
	patchItem.Value =
		json.RawMessage(cpuVal)

	return patchItem

}

func patchContainer(cpuAnnotation types.CPUAnnotation, patchList []patch, i int, c *corev1.Container) ([]patch, error) {
	var patchItem patch
	sharedCPUTime := cpuAnnotation.ContainerSharedCPUTime(c.Name, poolConf)

	glog.V(2).Infof("Adding patches")

	// podinfo volumeMount
	patchItem.Op = "add"
	patchItem.Path = "/spec/containers/" + strconv.Itoa(i) + "/volumeMounts/-"
	patchItem.Value =
		json.RawMessage(`{"name":"podinfo","mountPath":"/etc/podinfo","readOnly":true}`)
	patchList = append(patchList, patchItem)

	// hostbin volumeMount. Location for process starter binary
	patchItem.Path = "/spec/containers/" + strconv.Itoa(i) + "/volumeMounts/-"
	patchItem.Value =
		json.RawMessage(`{"name":"hostbin","mountPath":"/opt/bin","readOnly":true}`)
	patchList = append(patchList, patchItem)

	//  device plugin config volumeMount.
	patchItem.Path = "/spec/containers/" + strconv.Itoa(i) + "/volumeMounts/-"
	patchItem.Value =
		json.RawMessage(`{"name":"cpu-dp-config","mountPath":"/etc/cpu-dp","readOnly":true}`)
	patchList = append(patchList, patchItem)

	// Container name to env variable
	contNameEnvPatch := `{"name":"CONTAINER_NAME","value":"` + c.Name + `" }`
	patchItem.Path = "/spec/containers/" + strconv.Itoa(i) + "/env"
	if len(c.Env) > 0 {
		patchItem.Path += "/-"
	} else {
		contNameEnvPatch = `[` + contNameEnvPatch + `]`
	}
	patchItem.Value = json.RawMessage(contNameEnvPatch)
	patchList = append(patchList, patchItem)

	// Overwrite entrypoint
	patchItem.Path = "/spec/containers/" + strconv.Itoa(i) + "/command"
	patchItem.Value = json.RawMessage(`[ "/opt/bin/process-starter" ]`)
	patchList = append(patchList, patchItem)

	// Add cpu limit if container requested shared pool cpus
	if sharedCPUTime > 0 {
		patchItem.Path = "/spec/containers/" + strconv.Itoa(i) + "/resources/limits/cpu"
		cpuVal := `"` + strconv.Itoa(sharedCPUTime) + `m"`
		patchItem.Value =
			json.RawMessage(cpuVal)
		patchList = append(patchList, patchItem)
	}
	// CPU pool recource request/limit from all pools of container
	for _, pool := range cpuAnnotation.ContainerPools(c.Name) {
		patchItem = cpuPoolResourcePatch(cpuAnnotation, pool, i, c.Name)
		patchList = append(patchList, patchItem)
	}

	return patchList, nil
}

func validateResource(resList corev1.ResourceList) error {
	resourceBaseNameLen := len(poolConf.ResourceBaseName)
	for key := range resList {
		compLen := min(len(key), resourceBaseNameLen)
		if string(key)[:compLen] == poolConf.ResourceBaseName {
			poolName := string(key)[resourceBaseNameLen+1:]
			if _, found := poolConf.Pools[poolName]; !found {
				return errors.New(poolName + " not found from pool config")
			}
		}
	}
	return nil
}

func validateContainerResourceSpec(c *corev1.Container) error {
	if err := validateResource(c.Resources.Limits); err != nil {
		return err
	}
	if err := validateResource(c.Resources.Requests); err != nil {
		return err
	}
	return nil
}

func mutatePods(ar v1beta1.AdmissionReview) *v1beta1.AdmissionResponse {
	glog.V(2).Info("mutating pods")
	var err error
	var patchList []patch

	podResource := metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	if ar.Request.Resource != podResource {
		glog.Errorf("expect resource to be %s", podResource)
		return nil
	}

	raw := ar.Request.Object.Raw
	pod := corev1.Pod{}
	deserializer := codecs.UniversalDeserializer()
	if _, _, err = deserializer.Decode(raw, nil, &pod); err != nil {
		glog.Error(err)
		return toAdmissionResponse(err)
	}

	reviewResponse := v1beta1.AdmissionResponse{}

	annotationName := annotationNameFromConfig()

	reviewResponse.Allowed = true

	// Mutate containers if cpu annotation exists.
	if podAnnotation, exists := pod.ObjectMeta.Annotations[annotationName]; exists {
		glog.V(2).Infof("mutatePod : Annotation %v", podAnnotation)
		cpuAnnotation := types.CPUAnnotation{}

		err = cpuAnnotation.Decode([]byte(podAnnotation), poolConf)
		if err != nil {
			glog.Errorf("Failed to decode pod annotation %v", err)
			return toAdmissionResponse(err)
		}
		containersToPatch := cpuAnnotation.Containers()
		glog.V(2).Infof("Patch containers %v", containersToPatch)

		// Patch container if needed. If no annotation for container, validate
		// possible CPU-Pooler resource requests/limits
		for i, c := range pod.Spec.Containers {
			if isContainerPatchNeeded(c.Name, containersToPatch) {
				patchList, err = patchContainer(cpuAnnotation, patchList, i, &c)
				if err != nil {
					return toAdmissionResponse(err)
				}
			}
		}
	}
	// Validate CPU-Pooler resource requests/limits for containers
	for _, c := range pod.Spec.Containers {
		err = validateContainerResourceSpec(&c)
		if err != nil {
			return toAdmissionResponse(err)
		}
	}

	// Add volumes if any container was patched
	if len(patchList) > 0 {
		var patchItem patch
		patchItem.Op = "add"

		// podinfo volume
		patchItem.Path = "/spec/volumes/-"
		patchItem.Value = json.RawMessage(`{"name":"podinfo","downwardAPI": { "items": [ { "path" : "annotations","fieldRef":{ "fieldPath": "metadata.annotations"} } ] } }`)
		patchList = append(patchList, patchItem)
		// hostbin volume
		patchItem.Path = "/spec/volumes/-"
		patchItem.Value = json.RawMessage(`{"name":"hostbin","hostPath":{ "path":"/opt/bin"} }`)
		patchList = append(patchList, patchItem)

		// cpu dp configmap volume
		patchItem.Path = "/spec/volumes/-"
		patchItem.Value = json.RawMessage(`{"name":"cpu-dp-config","configMap":{ "name":"cpu-dp-configmap"} }`)
		patchList = append(patchList, patchItem)

		patch, err := json.Marshal(patchList)
		if err != nil {
			glog.Errorf("Patch marshall error %v:%v", patchList, err)
			reviewResponse.Allowed = false
			return toAdmissionResponse(err)
		}
		reviewResponse.Patch = []byte(patch)
		pt := v1beta1.PatchTypeJSONPatch
		reviewResponse.PatchType = &pt
	}
	return &reviewResponse
}

func serveMutatePod(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if r.Body != nil {
		if data, err := ioutil.ReadAll(r.Body); err == nil {
			body = data
		}
	}

	// verify the content type is accurate
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/json" {
		glog.Errorf("contentType=%s, expect application/json", contentType)
		return
	}

	requestedAdmissionReview := v1beta1.AdmissionReview{}

	responseAdmissionReview := v1beta1.AdmissionReview{}

	deserializer := codecs.UniversalDeserializer()
	if _, _, err := deserializer.Decode(body, nil, &requestedAdmissionReview); err != nil {
		glog.Error(err)
		responseAdmissionReview.Response = toAdmissionResponse(err)
	} else {
		responseAdmissionReview.Response = mutatePods(requestedAdmissionReview)
	}

	responseAdmissionReview.Response.UID = requestedAdmissionReview.Request.UID

	respBytes, err := json.Marshal(responseAdmissionReview)

	if err != nil {
		glog.Error(err)
	}
	w.Header().Set("Content-Type", "application/json")

	if _, err := w.Write(respBytes); err != nil {
		glog.Error(err)
	}
}

func main() {
	var certFile string
	var keyFile string

	flag.StringVar(&certFile, "tls-cert-file", certFile, ""+
		"File containing the default x509 Certificate for HTTPS. (CA cert, if any, concatenated "+
		"after server cert).")
	flag.StringVar(&keyFile, "tls-private-key-file", keyFile, ""+
		"File containing the default x509 private key matching --tls-cert-file.")

	flag.Parse()

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		glog.Fatal(err)
		panic(1)
	}

	poolConf, err = types.ReadPoolConfig()
	if err != nil {
		glog.Errorf("Could not read poolconfig %v", err)
		panic(1)
	}

	http.HandleFunc("/mutating-pods", serveMutatePod)
	server := &http.Server{
		Addr:         ":443",
		TLSConfig:    &tls.Config{Certificates: []tls.Certificate{cert}},
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	server.ListenAndServeTLS("", "")
}
