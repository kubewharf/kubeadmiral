# multi-arch image building for kubeadmiral-controller-manager
FROM --platform=${BUILDPLATFORM} golang:1.19 as builder
ARG TARGETPLATFORM GOPROXY
ADD . /build
WORKDIR /build/
RUN CGO_ENABLED=0 BUILD_PLATFORMS=${TARGETPLATFORM} GOPROXY=${GOPROXY} make build

FROM --platform=${TARGETPLATFORM} alpine:3.17
ARG TARGETOS TARGETARCH REGION
RUN if [ "${REGION}" = "cn" ]; then echo -e http://mirrors.ustc.edu.cn/alpine/v3.17/main/ > /etc/apk/repositories; fi
RUN apk add --no-cache ca-certificates bash && update-ca-certificates
COPY --from=builder /build/output/bin/${TARGETOS}/${TARGETARCH}/kubeadmiral-controller-manager kubeadmiral-controller-manager
ENTRYPOINT ["/kubeadmiral-controller-manager"]
