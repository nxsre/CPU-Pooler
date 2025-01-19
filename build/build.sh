docker build --platform linux/arm64 --build-arg http_proxy=$http_proxy --build-arg https_proxy=$https_proxy -t swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpu-device-webhook:$1 -f build/Dockerfile.webhook  .
docker build --platform linux/arm64 --build-arg http_proxy=$http_proxy --build-arg https_proxy=$https_proxy -t swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpusetter:$1 -f build/Dockerfile.cpusetter  .
docker build --platform linux/arm64 --build-arg http_proxy=$http_proxy --build-arg https_proxy=$https_proxy -t swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpudp:$1 -f build/Dockerfile.cpudp  .

docker push swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpu-device-webhook:$1
docker push swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpusetter:$1
docker push swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpudp:$1

docker image save swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpu-device-webhook:$1 swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpusetter:$1 swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpudp:$1 -o cpu_image_$1.tar

echo "kubectl set image deployment/cph-cpu-dev-pod-mutator-deployment cpu-dev-pod-mutator=swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpu-device-webhook:$1  -n kube-plugins"
echo "kubectl set image ds/cph-cpu-setter cpu-device-plugin=swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpusetter:$1 -n kube-plugins"
echo "kubectl set image ds/cph-cpu-device-plugin cpu-device-plugin=swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpudp:$1  -n kube-plugins"


docker tag swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpu-device-webhook:$1  100.76.9.59:8888/cloudphone/cph-cpu-device-webhook:$1
docker tag swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpusetter:$1 100.76.9.59:8888/cloudphone/cph-cpusetter:$1
docker tag swr.cn-south-1.myhuaweicloud.com/bonc/cph-cpudp:$1 100.76.9.59:8888/cloudphone/cph-cpudp:$1
docker push 100.76.9.59:8888/cloudphone/cph-cpu-device-webhook:$1
docker push 100.76.9.59:8888/cloudphone/cph-cpusetter:$1
docker push 100.76.9.59:8888/cloudphone/cph-cpudp:$1
echo "kubectl set image deployment/cpu-dev-pod-mutator-deployment cpu-dev-pod-mutator=100.76.9.59:8888/cloudphone/cph-cpu-device-webhook:$1  -n kube-plugins"
echo "kubectl set image ds/cpu-setter cpu-device-plugin=100.76.9.59:8888/cloudphone/cph-cpusetter:$1 -n kube-plugins"
echo "kubectl set image ds/cpu-device-plugin cpu-device-plugin=100.76.9.59:8888/cloudphone/cph-cpudp:$1  -n kube-plugins"



