docker build --build-arg http_proxy=$http_proxy --build-arg https_proxy=$https_proxy -t cpu-device-webhook -f build/Dockerfile.webhook  .
docker build --build-arg http_proxy=$http_proxy --build-arg https_proxy=$https_proxy -t cpusetter -f build/Dockerfile.cpusetter  .
docker build --build-arg http_proxy=$http_proxy --build-arg https_proxy=$https_proxy -t cpudp -f build/Dockerfile.cpudp  .
docker save cpusetter cpudp cpu-device-webhook > cpu-pooler_images.tar
