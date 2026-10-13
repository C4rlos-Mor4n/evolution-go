# evolution-go 0.7.2 — community PRs integrated (Krionix)

Build derivada, **no es una release oficial upstream**.

- Base: upstream `main` = release `0.7.2` (igual al `main` de este fork).
- Ramas:
  - `krionix/community-0.7.2`: 23 PRs de la comunidad más 5 commits propios (`fix(krionix)`).
  - `krionix/community-0.7.2-whatsmeow`: lo anterior **+ #190** (whatsmeow 20260904, Go 1.26). Está separado porque toca la capa de protocolo de todos los tenants: conviene probarlo primero en una o dos instancias (canary).
- Imágenes candidatas (locales, no publicadas): `evolution-go-krionix:0.7.2-community.1-rc1` y `evolution-go-krionix:0.7.2-community.1-whatsmeow-rc1`.
- Revisión: triage de los 64 PRs abiertos de upstream; revisión independiente del diff final (sus hallazgos están corregidos).
- Comparación con la imagen de producción `c4rlosmor4n/evolution-go-krionix:0.7.2-stability-rc1@sha256:2733bc13…`:
  - Su código no está en el fork y el binario no trae metadatos de VCS. Por eso comparé los símbolos de funciones (`go tool nm`) de su binario contra upstream 0.7.2 y contra esta rama.
  - Resultado: **rc1 = upstream 0.7.2 + #117 + #136 + #137 + escrituras serializadas en el websocket + un `ClientMapsMu` global sobre los mapas compartidos**. Usa el mismo whatsmeow y no quita ninguna función de upstream.

  | Arreglo en rc1 | Símbolos en rc1 | En esta rama |
  |---|---|---|
  | #117, container de sqlstore compartido | `sharedAuthContainer`, `getAuthContainer` | ✅ los mismos símbolos |
  | #136, settings parciales y scope de instancia | `applyConnectSettings`, `UpdateConnectSettings`, `buildAdvancedSettingsUpdates` | ✅ los mismos símbolos |
  | #137, menciones | `setMessageMentionedJIDs`, `participantMentionJID` | ✅ los mismos símbolos |
  | Escrituras serializadas en el websocket | `wsConn.writeJSON` | ✅ ampliado con #181: mutex por cliente, write deadline y varios suscriptores |
  | Mutex de mapas | `ClientMapsMu` (un RWMutex global) | ✅ ampliado con #196: `safemap` tipado, que el compilador obliga a usar en cada acceso |

  Reemplazar rc1 por esta rama **no pierde ningún arreglo** y suma 18 PRs más y 5 arreglos propios.

## Estabilidad y concurrencia

| PR | Qué arregla |
|---|---|
| #117 | Fuga de conexiones a Postgres: un solo `sqlstore.Container` compartido, con pool limitado. Un fallo de BD no se cachea, así que se reintenta. |
| #196 | `fatal error: concurrent map` en los 3 mapas compartidos (`safemap` con tipos). |
| #125 | Pánico con el evento Archive. |
| #126 | Conexiones zombie: reinicio tras keepalive timeouts. Lleva un guard de "reconexión en curso" compartido con el camino de `Disconnected`. |
| #192 | Un `Disconnected` durante el emparejamiento por QR ya no reinicia el dispositivo. Le quitamos la reconexión en caliente que traía, porque duplicaba la rotación del QR. |
| #149 | `GET /instance/qr` ya no mata una sesión que ya está vinculada. |
| #187 | Desconectar ya no borra las suscripciones de eventos (antes, tras reconectar, se perdían todos los webhooks). |
| #144 | Recuperación de errores de sincronización del app-state, con cooldown. |
| #156 | Opcional, con `REREQUEST_FROM_PHONE`: pide al teléfono los mensajes que no se pudieron descifrar. |
| #143 | NATS no intenta conectarse a localhost si la URL está vacía. |
| #181 | Websocket: entrega a todos los suscriptores, sin pánicos por escrituras concurrentes y con write deadline. |
| propio | La presencia AlwaysOnline tiene su propio stop channel y una sola goroutine por cliente; ya no le roba la señal de kill a StartClient (core de #213). |
| propio | Si RabbitMQ, NATS o el websocket fallan, el webhook se entrega igual. |
| propio | Las señales de kill nunca bloquean: canal nil o runtime ya terminado no dejan colgada una request HTTP ni el handler de eventos. |
| propio | El loop de un runtime reemplazado se detiene. El runtime vivo vuelve a fijar su `MyClient` cuando dos `StartClient` concurrentes se pisan. |

## Mensajes

| PR | Qué aporta |
|---|---|
| #122 | Las ediciones cifradas (secret-encrypted) se descifran y se entregan; antes se descartaban. |
| #130 | JIDs canónicos (sin "+") en avatar, borrar para todos y editar. |
| #137 | Menciones en media con caption y en mensajes interactivos; corrige un pánico. |
| #212 | Ancho, alto y orientación EXIF en imágenes, para que iOS no las recorte. |
| #207 | Thumbnail de alta calidad en link previews; si el preview falla, el envío sigue. |
| #166 | Los stickers WebP animados se envían tal cual. |
| #147 | `viewOnce` en `/send/media`. |
| #218 | El history sync bajo demanda se manda al dispositivo primario. |
| #191 | Los mensajes enviados desde el teléfono quedan en el chat real. El `Chat` solo se reescribe si es nuestro propio JID. |
| #180 | La validación de participantes de grupo acepta arrays. |
| #179 | `POST /user/lid` (resolver un LID a número de teléfono). |

## Seguridad

| PR | Qué arregla |
|---|---|
| #136 | `/instance/:id/advanced-settings` aceptaba el token de cualquier instancia para leer o escribir otra (fuga entre tenants). Además, `/instance/connect` con body parcial ya no borra webhook ni eventos. |

## #190 (solo en la rama `-whatsmeow`)

- whatsmeow `b572e5bcb92b` → `20260904121843-28bfe537ea6a`: arregla el loop de reconexión por stream-error que traba la entrega de la cola offline.
- Go 1.25 → 1.26; el Dockerfile queda fijado en `golang:1.26.9-alpine`.
- `SetStatusMessage` se adaptó a `types.SetStatusInput`.
- No se aplicó su hunk de sqlstore: ya lo cubre #117.

## Descartados (motivo)

- **Duplicados:**
  - Fuga de sqlstore: #102, #131, #142, #174, #178, #194, #199, #200, #206.
  - Evento Archive: #159, #177, #195.
  - Mapas compartidos: #127, #167.
  - Keepalive: #160.
  - Ediciones cifradas: #128, #161, #153.
  - JIDs canónicos: #163.
  - Websocket: #135.
  - History sync: #133, #150.
  - Stickers: #151.
- **Reescrituras grandes o en conflicto:** #145 y #154 (ciclo de vida), #197 (backoff de 5 a 30 min), #120 y #121 (~1.5k líneas, mayormente regeneración de swagger).
- **Funcionalidades no necesarias o riesgosas:**
  - #152: deja la cuenta "available" y suprime las notificaciones del teléfono.
  - #129 y #162: guardar contactos en la libreta del teléfono.
  - #141: llamadas.
  - #182 y #184: UI.
  - #90 y #132: endpoints nuevos basados en develop.
- **Irrelevantes para nosotros:** #208 (formato de números de Brasil), #201 y #202 (solo README).
- **#213:** se portó solo su núcleo (ver la tabla de estabilidad).

## Cambios de contrato visibles para consumidores

1. `/instance/connect`:
   - Si `webhookUrl`, `rabbitmqEnable`, `natsEnable` o `websocketEnable` llegan vacíos, o si `subscribe` llega como `[]`, se conserva lo guardado. Para desactivar hay que mandar `"disabled"` o `"false"`.
   - Un `subscribe` sin eventos válidos devuelve 400.
2. `/instance/disconnect` ya no borra `events`.
3. `GET /instance/qr` con sesión vinculada responde "session already logged in".
4. advanced-settings: el path tiene que coincidir con la instancia del token (si no, 401/403), y el PUT es parcial.
5. Eventos de conexión nuevos: `KeepAliveTimeout` y `KeepAliveRestored`.
6. Evento Message:
   - Las ediciones cifradas llegan, con claves nuevas: `IsEdit`, `IsRevoke`, `messageType`, `decryptFailed`, `Recipient`.
   - En mensajes enviados desde el teléfono, `Info.Chat` es el destino real (puede ser `@lid`).
7. `POST /user/lid` es nuevo.

## Pendiente / conocido

- El cap del pool de #117 está fijo en 20 conexiones y el `Upgrade` del container no tiene timeout. Funciona, pero si Postgres no responde al arrancar, los `StartClient` quedan esperando.
- El loop de StartClient todavía relee el canal de kill en cada vuelta. El guard de "superseded" lo hace converger, pero puede haber sockets duplicados transitorios si dos `connect` se pisan.
- **QA real pendiente:**
  - Emparejamiento por QR y por pairing code.
  - Reconexión tras cortar la red.
  - Envío de media.
  - Webhooks.
  - Primero en demo; la rama `-whatsmeow` en canary.

## Verificación

- `go build ./...`, `go vet ./...`, `go test ./...`: PASS en las dos ramas (Go 1.25.0 y 1.26.9).
- `go test -race` sobre safemap, whatsmeow, instance y events: PASS.
- gofmt: PASS en los archivos que cambiamos. `pkg/core/c0.go` ya viene sin formatear desde upstream.
- Smoke test de las dos imágenes con Postgres aislado: arrancan, `/server/ok` responde 200, cero pánicos y cero reinicios.
- La API funcional exige activar la licencia de upstream (`LICENSE_REQUIRED`), así que no se probó de forma automática.
