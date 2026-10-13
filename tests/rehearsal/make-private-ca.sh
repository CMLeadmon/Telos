#!/usr/bin/env bash
# make-private-ca.sh OUTDIR DOMAIN — a throw-away private CA and a server certificate.
#
# REHEARSAL ONLY. It mints the CA and leaf the private-CA overlay serves, so a
# reference VM can present platform-trusted HTTPS without a public DNS name. The
# resulting CA must be installed in the verifier's operating-system trust store
# (update-ca-certificates); nothing in Telos waives validation. Production nodes get
# their certificate from public ACME or from the operator's own CA.
#
# Writes OUTDIR/{ca.crt,ca.key,server.crt,server.key}. server.crt holds the leaf
# followed by the CA. Keys are mode 0600. Refuses to overwrite.
set -euo pipefail
out="${1:?usage: make-private-ca.sh OUTDIR DOMAIN}"; domain="${2:?domain required}"
[[ "$out" = /* ]] || { echo "OUTDIR must be absolute" >&2; exit 2; }
[[ -e "$out/ca.crt" || -e "$out/server.crt" ]] && { echo "make-private-ca: $out already holds a CA or certificate; refusing to overwrite" >&2; exit 1; }
mkdir -p "$out"; chmod 0700 "$out"; cd "$out"; umask 077
openssl req -x509 -newkey rsa:3072 -nodes -keyout ca.key -out ca.crt -subj "/CN=Telos Rehearsal CA" -days 30 \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" -addext "subjectKeyIdentifier=hash" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -keyout server.key -out server.csr -subj "/CN=$domain" >/dev/null 2>&1
printf 'subjectAltName=DNS:%s\nbasicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectKeyIdentifier=hash\nauthorityKeyIdentifier=keyid\n' "$domain" >server.ext
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out server.leaf -days 30 -extfile server.ext >/dev/null 2>&1
cat server.leaf ca.crt >server.crt
rm -f server.csr server.ext server.leaf ca.srl
chmod 0644 ca.crt server.crt; chmod 0600 ca.key server.key
echo "make-private-ca: $out/server.crt for $domain (CA: $out/ca.crt)"
