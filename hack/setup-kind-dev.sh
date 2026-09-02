#!/usr/bin/env bash
# Recreate the local kind cluster with ingress-nginx, mkcert TLS, and CRDs.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CLUSTER="${KIND_DEV_CLUSTER:-data-platform-dev}"
CONFIG="${KIND_DEV_CONFIG:-config/kind/kind-data-platform-dev.yaml}"
INGRESS_MANIFEST="${KIND_INGRESS_MANIFEST:-config/kind/deploy-ingress-nginx.yaml}"
INGRESS_RESOURCES="${KIND_INGRESS_RESOURCES:-config/kind/ingresses.yaml}"
DOMAIN="${KIND_DEV_DOMAIN:-data-platform.local}"
CONTEXT="${KIND_DEV_CONTEXT:-kind-${CLUSTER}}"
KIND_BIN="${KIND:-kind}"
KUBECTL_BIN="${KUBECTL:-kubectl}"
CERT_DIR="${KIND_CERT_DIR:-${ROOT}/bin/certs}"
KUBECONFIG_FILE="${KIND_KUBECONFIG:-${ROOT}/bin/kubeconfig-${CLUSTER}}"
TLS_SECRET="${KIND_TLS_SECRET:-data-platform-tls}"
NAMESPACES=(keycloak lakekeeper trino)
HOSTS=(keycloak lakekeeper trino)

abs_from_root() {
	case "$1" in
	/*) printf '%s\n' "$1" ;;
	*) printf '%s\n' "${ROOT}/$1" ;;
	esac
}

CONFIG="$(abs_from_root "${CONFIG}")"
INGRESS_MANIFEST="$(abs_from_root "${INGRESS_MANIFEST}")"
INGRESS_RESOURCES="$(abs_from_root "${INGRESS_RESOURCES}")"

need() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "error: $1 is required" >&2
		exit 1
	fi
}

need "${KIND_BIN}"
need "${KUBECTL_BIN}"
need mkcert
need docker

kc() {
	"${KUBECTL_BIN}" --kubeconfig "${KUBECONFIG_FILE}" "$@"
}

echo "==> Recreating kind cluster ${CLUSTER}"
if "${KIND_BIN}" get clusters 2>/dev/null | grep -qx "${CLUSTER}"; then
	"${KIND_BIN}" delete cluster --name "${CLUSTER}"
fi
mkdir -p "$(dirname "${KUBECONFIG_FILE}")"
if ! "${KIND_BIN}" create cluster --config "${CONFIG}" --kubeconfig "${KUBECONFIG_FILE}"; then
	echo "error: kind create failed. If the error is about bind:80 or bind:443, stop whatever is using those ports on the host." >&2
	exit 1
fi
"${KIND_BIN}" export kubeconfig --name "${CLUSTER}" --kubeconfig "${KUBECONFIG_FILE}"
"${KIND_BIN}" export kubeconfig --name "${CLUSTER}"

echo "==> Installing ingress-nginx"
kc apply -f "${INGRESS_MANIFEST}"
kc -n ingress-nginx rollout status deployment/ingress-nginx-controller --timeout=180s

echo "==> Issuing mkcert wildcard for *.${DOMAIN}"
mkdir -p "${CERT_DIR}"
mkcert -install
mkcert -cert-file "${CERT_DIR}/tls.crt" -key-file "${CERT_DIR}/tls.key" \
	"*.${DOMAIN}" "${DOMAIN}" localhost 127.0.0.1

echo "==> Creating namespaces and TLS secrets"
for ns in "${NAMESPACES[@]}"; do
	kc create namespace "${ns}" --dry-run=client -o yaml | kc apply -f -
	kc create secret tls "${TLS_SECRET}" \
		--cert="${CERT_DIR}/tls.crt" --key="${CERT_DIR}/tls.key" \
		-n "${ns}" --dry-run=client -o yaml | kc apply -f -
done

echo "==> Applying Ingresses"
kc apply -f "${INGRESS_RESOURCES}"

echo "==> Installing DataPlatform CRDs"
make -C "${ROOT}" install KUBECONFIG="${KUBECONFIG_FILE}"

HOSTS_LINE="127.0.0.1"
for host in "${HOSTS[@]}"; do
	HOSTS_LINE+=" ${host}.${DOMAIN}"
done

if grep -q "keycloak.${DOMAIN}" /etc/hosts 2>/dev/null; then
	echo "==> /etc/hosts already has ${DOMAIN} entries"
else
	echo "==> Add these names to /etc/hosts:"
	echo "    ${HOSTS_LINE}"
	if [ "${KIND_UPDATE_HOSTS:-}" = "1" ]; then
		echo "${HOSTS_LINE}" | sudo tee -a /etc/hosts >/dev/null
		echo "    appended with sudo (KIND_UPDATE_HOSTS=1)"
	fi
fi

echo
echo "Kind cluster ${CLUSTER} is ready (kubeconfig ${KUBECONFIG_FILE}, context ${CONTEXT})."
echo "Next:"
echo "  make run"
echo "  kubectl --context ${CONTEXT} apply -f config/samples/dataplatform_v1alpha1_local.yaml"
echo "Then open:"
echo "  https://keycloak.${DOMAIN}"
echo "  https://lakekeeper.${DOMAIN}"
echo "  https://trino.${DOMAIN}"
echo "Admin password (after Keycloak is Ready):"
echo "  kubectl --context ${CONTEXT} get secret keycloak-admin -n keycloak -o jsonpath='{.data.password}' | base64 -d; echo"
