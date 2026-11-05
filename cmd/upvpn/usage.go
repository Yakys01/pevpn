package main

import (
	"fmt"
	"io"
)

const usageText = `upvpn - usa el proxy VPN de tus extensiones de Brave desde fuera del navegador.

Uso:
  upvpn serve [--port N] [--bind IP]      Levanta un proxy HTTP local (default 127.0.0.1:8888).
  upvpn <comando...>                      Ejecuta un comando con http_proxy apuntando al proxy.
  upvpn --show                            Muestra proveedores, endpoints y estado.
  upvpn --json                            Igual que --show, en JSON.
  upvpn --check                           Verifica el proxy en cada proveedor (HTTPS).
  upvpn --self-test                       Igual que --check, mas HTTP plano.
  upvpn --print-config                    Muestra la configuracion efectiva (defaults + archivo + flags).

Flags globales:
  --provider NAME   Usa solo un proveedor (urbanvpn | tunnelbearvpn | vpnbrowser).
  --config PATH     Archivo de configuracion (default ~/.config/upvpn/config.json).

Variables de entorno:
  UPVPN_PROVIDER, UPVPN_BIND, UPVPN_PORT, UPVPN_PROFILE_DIR, UPVPN_EXTENSION_ID,
  UPVPN_CONFIG, UPVPN_BRAVE_ROOT.

Ejemplos:
  upvpn serve --port 8888
  upvpn curl -s https://api.ipify.org
  upvpn --check
  UPVPN_PROVIDER=tunnelbearvpn upvpn --show
`

func usage(w io.Writer) {
	fmt.Fprint(w, usageText)
}
