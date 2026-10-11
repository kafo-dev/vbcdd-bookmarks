#!/bin/sh
# Build the tool and install it into a Termux home: the binary, the three
# hook names, the widget shortcut, and a configuration template.

set -eu

repo_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
home=${HOME:?HOME is not set}
bin_dir="${home}/bin"
config_dir="${home}/.config/vbcdd/bookmarks"
config_file="${config_dir}/config.toml"
shortcut_dir="${home}/.shortcuts"

if ! command -v go >/dev/null 2>&1; then
	echo "install: a Go toolchain builds this program; run 'pkg install golang'" >&2
	exit 1
fi

echo "install: building into ${bin_dir}/bookmarks"
mkdir -p "${bin_dir}" "${config_dir}" "${shortcut_dir}"
chmod 700 "${config_dir}"
(
	cd "${repo_dir}"
	go build -o "${bin_dir}/bookmarks" .
)

# Termux:API calls the share hook by name from ~/bin, and the two other
# names are the commands a key binding or a shell reaches for.
for name in bookmarks-add bookmarks-pick termux-url-opener; do
	ln -sf bookmarks "${bin_dir}/${name}"
done

install -m 700 "${repo_dir}/shortcuts/Bookmarks" "${shortcut_dir}/Bookmarks"

if [ -e "${config_file}" ]; then
	echo "install: keeping the existing ${config_file}"
else
	umask 077
	cat >"${config_file}" <<'TOML'
url = "https://webdav.example.org/"
file = "bookmarks.xbel"
user = "username"
password = "password"
TOML
	chmod 600 "${config_file}"
	echo "install: wrote ${config_file}; fill in the endpoint before the first run" >&2
fi

# The share hook lives at a fixed path, but every other name needs the
# directory on PATH.
case ":${PATH}:" in
*":${bin_dir}:"*) ;;
*) echo "install: add ${bin_dir} to PATH to call bookmarks by name" >&2 ;;
esac

"${bin_dir}/bookmarks" version
echo "install: done"
