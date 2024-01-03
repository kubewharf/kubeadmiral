# multi-arch image building for kubeadmiral-controller-manager
FROM --platform=${BUILDPLATFORM} golang:1.19 as builder
ARG TARGETPLATFORM GOPROXY BUILD_FLAGS COMPENT
ADD . /build
WORKDIR /build/
RUN BUILD_PLATFORMS=${TARGETPLATFORM} GOPROXY=${GOPROXY} BUILD_FLAGS=${BUILD_FLAGS} TARGET_NAME=${COMPENT} make build

FROM --platform=${TARGETPLATFORM} debian:buster
ARG TARGETOS TARGETARCH REGION COMPENT
RUN if [ "${REGION}" = "cn" ]; then sed -i 's#http://deb.debian.org#http://mirrors.tuna.tsinghua.edu.cn#g' /etc/apt/sources.list; fi
RUN apt-get update -y && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /build/output/bin/${TARGETOS}/${TARGETARCH}/${COMPENT} ${COMPENT}
ENTRYPOINT ["/${COMPENT}"]
