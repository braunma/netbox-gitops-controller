#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Bring up a throwaway NetBox from source on the local machine, for running
# tests/e2e/run.sh without a container runtime.
#
# The GitLab e2e job does not use this: there NetBox, PostgreSQL and Redis run
# as CI services. Use this on a workstation, or on a shell-executor runner that
# has no Docker daemon.
#
# Requires: apt-based Linux, root, and outbound access to PyPI and GitHub.
# Prints the NETBOX_URL / NETBOX_TOKEN to export when it is done.
set -euo pipefail

NETBOX_VERSION="${NETBOX_VERSION:-v4.6.7}"
NETBOX_ROOT="${NETBOX_ROOT:-/opt/netbox}"
NETBOX_PORT="${NETBOX_PORT:-8000}"
# Throwaway credentials for a disposable instance. Do not reuse.
DB_PASSWORD="netbox"
TOKEN_PLAINTEXT="${NETBOX_E2E_TOKEN:-0123456789abcdef0123456789abcdef01234567}"

echo ">>> installing PostgreSQL, Redis and Python"
apt-get update -qq
apt-get install -y -qq --no-install-recommends \
  postgresql redis-server python3.12 python3.12-venv python3.12-dev build-essential libpq-dev git curl

echo ">>> starting PostgreSQL and Redis"
pg_ctlcluster 16 main start 2>/dev/null || true
redis-server --daemonize yes --port 6379 2>/dev/null || true
sleep 3

su - postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname='netbox'\"" | grep -q 1 || \
  su - postgres -c "psql -c \"CREATE DATABASE netbox;\" \
                        -c \"CREATE USER netbox WITH PASSWORD '${DB_PASSWORD}';\" \
                        -c \"ALTER DATABASE netbox OWNER TO netbox;\""

if [ ! -d "${NETBOX_ROOT}" ]; then
  echo ">>> cloning NetBox ${NETBOX_VERSION}"
  git clone --depth 1 --branch "${NETBOX_VERSION}" https://github.com/netbox-community/netbox "${NETBOX_ROOT}"
fi

cat > "${NETBOX_ROOT}/netbox/netbox/configuration.py" <<PYCONF
ALLOWED_HOSTS = ['*']
DATABASES = {'default': {'ENGINE': 'django.db.backends.postgresql', 'NAME': 'netbox',
    'USER': 'netbox', 'PASSWORD': '${DB_PASSWORD}', 'HOST': 'localhost', 'PORT': '5432'}}
REDIS = {'tasks': {'HOST': 'localhost', 'PORT': 6379, 'DATABASE': 0, 'SSL': False},
         'caching': {'HOST': 'localhost', 'PORT': 6379, 'DATABASE': 1, 'SSL': False}}
SECRET_KEY = 'e2e-only-secret-key-not-for-production-0123456789abcdef'
API_TOKEN_PEPPERS = {1: 'e2e-only-pepper-at-least-fifty-characters-long-0123456789'}
DEBUG = False
PYCONF

if [ ! -d "${NETBOX_ROOT}/venv" ]; then
  echo ">>> installing NetBox dependencies (this takes a few minutes)"
  python3.12 -m venv "${NETBOX_ROOT}/venv"
  "${NETBOX_ROOT}/venv/bin/pip" install -q --upgrade pip
  "${NETBOX_ROOT}/venv/bin/pip" install -q -r "${NETBOX_ROOT}/requirements.txt"
fi

echo ">>> migrating"
"${NETBOX_ROOT}/venv/bin/python" "${NETBOX_ROOT}/netbox/manage.py" migrate >/dev/null

echo ">>> creating superuser and API token"
# A v1 token is minted directly: the plaintext is what the client sends as
# "Token <plaintext>". Passing only a key is rejected on NetBox 4.5+.
"${NETBOX_ROOT}/venv/bin/python" "${NETBOX_ROOT}/netbox/manage.py" shell -c "
from users.models import User, Token
u, _ = User.objects.get_or_create(username='admin')
u.is_superuser = True; u.set_password('admin'); u.save()
Token.objects.filter(user=u, version=1).delete()
Token.objects.create(user=u, version=1, plaintext='${TOKEN_PLAINTEXT}')
" >/dev/null

pgrep -f "manage.py runserver" >/dev/null || \
  (cd "${NETBOX_ROOT}" && nohup ./venv/bin/python netbox/manage.py runserver "127.0.0.1:${NETBOX_PORT}" \
     --insecure --noreload >/tmp/netbox-e2e.log 2>&1 &)

for _ in $(seq 1 30); do
  curl -sS --noproxy '*' -o /dev/null "http://127.0.0.1:${NETBOX_PORT}/api/" && break
  sleep 2
done

cat <<MSG

>>> NetBox is up. Run the end-to-end tests with:

    export NETBOX_URL=http://127.0.0.1:${NETBOX_PORT}
    export NETBOX_TOKEN=${TOKEN_PLAINTEXT}
    make e2e
MSG
