# sboe-go

Recolector de subastas públicas de España. Descarga las subastas de varias webs, las normaliza a un
formato común y las guarda en una base de datos SQLite con historial.

Es la reescritura en Go de `sboe` (Node/TypeScript + Google Sheets).

Incluye una interfaz web con dos partes:

- **Platform** (`/app`): cada usuario explora y filtra las subastas, guarda filtros y configura
  alertas por email.
- **Admin** (`/admin`): programar cada cuánto corre cada job, ejecutarlos a mano, ver el historial y
  gestionar las cuentas de usuario.

## Fuentes

| Job | Web | Qué obtiene |
|---|---|---|
| `boe` | [subastas.boe.es](https://subastas.boe.es) | Subastas en "Próxima apertura" y "Celebrándose" (listado + detalle de cada subasta) |
| `procuradores` | [subastasprocuradores.com](https://www.subastasprocuradores.com) | Todas las subastas de España (listado paginado + detalle) |
| `ss_regulares` | [Seguridad Social: subastas](https://w6.seg-social.es/subastas/SubaSeControladorInter?opcion=6&avanzada=1) | Bienes embargados (inmuebles, vehículos, muebles) |
| `ss_adjudicacion` | [Seguridad Social: inmuebles](https://w6.seg-social.es/vinmWeb/VinmSeControladorInter?opcion=33&T=4) | Inmuebles en venta por adjudicación directa |

### Cómo se descargan

- **Se guarda sobre la marcha**, en lotes de 50 subastas. Si un job falla a mitad, lo descargado
  queda guardado (y se avisa de las nuevas); solo no se marcan como retiradas las que faltan.
- **Incremental**: el detalle de una subasta (la parte lenta) solo se pide si es nueva, si cambió de
  estado en el listado o si su detalle tiene más de 7 días. El resto solo se marca como “vista”.
  Una ejecución **completa** (`run -full` o el botón “Completa” del admin) vuelve a pedirlo todo.
- **En paralelo** entre webs distintas (BOE, Procuradores y Seguridad Social a la vez); los dos jobs
  de la Seguridad Social comparten servidor y van uno tras otro. Dentro de cada web las peticiones
  siguen siendo de una en una, con pausa aleatoria.

La primera carga (base vacía) tarda bastante —sobre todo el BOE: ~1300 subastas con 2 páginas de
detalle cada una—; las siguientes, pocos minutos.

## Requisitos

- Go 1.26+
- No necesita CGO: el driver SQLite (`modernc.org/sqlite`) es Go puro.
- Opcional: `sqlite3` CLI para consultar la base de datos, Docker.

## Puesta en marcha

```sh
cp .env.example .env        # ajusta los valores (SMTP, URL pública…)

# 1. crea el primer administrador
go run ./cmd/sboe createuser -email admin@tudominio.com -password 'una-contraseña' -admin

# 2. arranca el servidor web (incluye los jobs programados y las alertas)
go run ./cmd/sboe serve
```

Abre <http://localhost:8080>, entra con el admin y:

1. En **Jobs** pulsa “Ejecutar ahora” en cada job para cargar datos (o configura su horario).
2. En **Usuarios** crea las cuentas de los usuarios.

`.env` se carga automáticamente desde el directorio actual (las variables del entorno tienen
prioridad; otra ruta con `SBOE_ENV_FILE`).

## Subcomandos

| Comando | Qué hace |
|---|---|
| `sboe serve` | Servidor web + jobs programados + alertas por email. |
| `sboe run [-source=a,b] [-full] [-dump-html dir] [-v] [-no-alerts]` | Ejecuta los jobs una vez y termina (es el comando por defecto). `-full` descarga el detalle de todo. También envía las alertas “cuando corran los jobs”, salvo con `-no-alerts`. |
| `sboe createuser -email x -password y [-admin]` | Crea un usuario desde la terminal. |

```sh
go run ./cmd/sboe run                                  # todos los jobs, una vez
go run ./cmd/sboe run -source=boe,ss_regulares         # solo algunos
go run ./cmd/sboe run -source=boe -dump-html ./dump -v # depurar parsers
```

`run` sale con código `1` si algún job falló; los demás se ejecutan igualmente.

## Interfaz web

### Platform (todos los usuarios)

- **Subastas**: explorador con filtros que se aplican al momento: texto o ID (`SB-000123`), fuente,
  tipo de bien (vivienda, garaje, local, solar…), estado, provincia, localidad, rangos de valor y tasación, puja mínima y cantidad reclamada máximas,
  % reclamado sobre el valor, vivienda habitual, fecha de fin e incluir subastas ya retiradas.
  Ordenación y paginación. La URL refleja el filtro, así que se puede compartir.
- **Guardar como filtro / alerta**: guarda los criterios con un nombre y una alerta opcional:
  - **Cuando corran los jobs**: al terminar cada job, si entraron subastas *nuevas* que cumplen el
    filtro, se envía un email con ellas.
  - **Diaria a una hora**: una vez al día, a la hora elegida (hora de España), un email con las
    subastas nuevas desde la alerta anterior. Si no hay novedades no se envía nada.
  - El correo de destino lo elige el usuario (por defecto el de su cuenta). Cada filtro tiene un botón
    “Enviar prueba”.
- **Mi cuenta**: cambiar la contraseña.

### Admin

- **Jobs**: estado de cada job (horario, próxima ejecución, última ejecución con subastas y nuevas),
  botón “Ejecutar ahora” (en segundo plano; la tabla se refresca sola) e historial de ejecuciones.
  En “Configurar” se elige el horario: todos los días a una hora, cada N horas, ciertos días de la
  semana o una expresión cron; y se activa o pausa la ejecución automática. Un job nunca se ejecuta
  dos veces a la vez.
- **Usuarios**: crear usuarios (email, contraseña inicial, rol usuario/admin), activar/desactivar,
  dar o quitar admin y poner una contraseña nueva. Un admin no puede modificarse a sí mismo.

Los jobs vienen **pausados** por defecto (horario sugerido: todos los días a las 05:09).

### Seguridad

Contraseñas con bcrypt; sesión en cookie `HttpOnly` + `SameSite=Lax` (y `Secure` con
`SBOE_COOKIE_SECURE=1`) que dura 30 días y de la que en la base solo se guarda el hash; token CSRF en
todas las peticiones que modifican datos; límite de 10 intentos de login fallidos cada 15 minutos por
email. Pon el servidor detrás de HTTPS (Caddy, nginx…) si se expone a Internet.

## Variables de entorno

Ver `.env.example`.

| Variable | Default | Descripción |
|---|---|---|
| `SBOE_DB_PATH` | `./data/sboe.db` | Ruta de la base de datos (se crea si no existe). |
| `SBOE_ADDR` | `:8080` | Dirección del servidor web. |
| `SBOE_BASE_URL` | `http://localhost:8080` | URL pública, para los enlaces de los emails. |
| `SBOE_COOKIE_SECURE` | `0` | `1` si se sirve por HTTPS. |
| `SBOE_TZ` | `Europe/Madrid` | Zona horaria de los horarios y las alertas diarias. |
| `SBOE_MIN_DELAY` / `SBOE_MAX_DELAY` | `1s` / `3s` | Pausa aleatoria entre peticiones a las webs. |
| `SBOE_RESEND_API_KEY` | — | [API key de Resend](https://resend.com/api-keys). Si está definida se envía por Resend y se ignora SMTP. |
| `SBOE_RESEND_FROM` | `onboarding@resend.dev` | Remitente, de un [dominio verificado](https://resend.com/domains) (`Alertas SBOE <alertas@tudominio.com>`). Con el valor por defecto Resend solo entrega a la cuenta dueña de la API key. |
| `SBOE_SMTP_HOST` | — | Servidor SMTP (`smtp.gmail.com`), alternativa a Resend. Sin Resend ni SMTP los emails solo se escriben en el log. |
| `SBOE_SMTP_PORT` | `587` | `587` (STARTTLS) o `465` (TLS). |
| `SBOE_SMTP_USER` / `SBOE_SMTP_PASS` | — | Cuenta y [contraseña de aplicación](https://myaccount.google.com/apppasswords) de Gmail. |
| `SBOE_SMTP_FROM` | `SBOE_SMTP_USER` | Remitente. |

## Docker

Con Docker Compose (lee `.env`; `SBOE_DB_PATH` y `SBOE_ADDR` se fijan en `compose.yaml`, así que el `.env` local sirve tal cual):

```sh
docker compose up -d --build                     # serve en http://localhost:8080
docker compose logs -f
docker compose run --rm sboe createuser -email admin@x.com -password 'secreto123' -admin
docker compose run --rm sboe run -source=boe     # ejecución puntual
docker compose down
```

La base de datos vive en `./data` (montado en `/data`). El contenedor corre como UID 1000; si tu usuario tiene otro UID, ajusta la propiedad de `./data` (`sudo chown -R 1000:1000 data`).

Sin Compose:

```sh
docker build -t sboe-go .
docker run -d --name sboe -p 8080:8080 -v "$PWD/data:/data" --env-file .env -e SBOE_DB_PATH=/data/sboe.db sboe-go
```

### Portainer (en otro servidor)

El stack `deploy/portainer-stack.yml` usa la imagen `cue2g/boe:latest` de Docker Hub y guarda
la base de datos en el volumen `sboe-data`.

1. **Construir la imagen para la arquitectura del servidor** (`uname -m` en el servidor):
   ```sh
   docker build -t cue2g/boe:latest .                                               # x86_64
   docker buildx build --platform linux/arm64 -t cue2g/boe:latest --load .          # aarch64 (Raspberry Pi, etc.)
   ```
2. **Subirla a Docker Hub:**
   ```sh
   docker push cue2g/boe:latest
   ```
3. En Portainer: *Stacks › Add stack*, nombre `sboe`, *Web editor*, y pegar el contenido de `deploy/portainer-stack.yml`.
4. En *Environment variables* definir al menos `SBOE_BASE_URL` (p.ej. `http://IP-del-servidor:8080`) y las
   `SBOE_RESEND_*` (o `SBOE_SMTP_*`). Con *Load variables from .env file* puedes subir tu `.env`; `SBOE_DB_PATH` se ignora (el stack la fija).
   `SBOE_PORT` cambia el puerto publicado (8080 por defecto).
5. *Deploy the stack*.
6. Crear el admin: *Containers › sboe › Console* (`/bin/sh`, *Connect*):
   ```sh
   sboe createuser -email admin@x.com -password 'secreto123' -admin
   ```

**Migrar la base de datos local (opcional):** copia `data/sboe.db` al servidor, para el stack y, en el servidor:
```sh
docker run --rm -v sboe-data:/data -v "$PWD":/src alpine sh -c 'cp /src/sboe.db /data/ && rm -f /data/sboe.db-* && chown 1000:1000 /data/sboe.db'
```
y vuelve a arrancar el stack.

**Actualizar a una versión nueva:** repetir los pasos 1 y 2 y en el stack pulsar *Update the stack* activando
*Re-pull image and redeploy* (se recrea el contenedor con la imagen nueva; los datos del volumen se conservan).

## Base de datos

Las migraciones están en `internal/store/migrations/` y se aplican solas al arrancar (versionadas con
`PRAGMA user_version`).

### `auctions`

Una fila por subasta, con clave única (`source`, `external_id`).

| Columna | Descripción |
|---|---|
| `id` / `public_id` | Id interno y su ID público legible, `SB-000123` (columna generada, estable). |
| `source` | Job de origen (`boe`, `procuradores`, `ss_regulares`, `ss_adjudicacion`). |
| `external_id` | Id de la subasta en la web de origen. |
| `asset_type` | Tipo unificado: `inmueble`, `vehiculo` o `mueble`. |
| `asset_subtype` | Subtipo unificado: `vivienda`, `garaje`, `trastero`, `local`, `nave`, `solar`, `finca_rustica`, `edificio`, `inmueble_otros`, `vehiculo`, `embarcacion`, `maquinaria`, `mueble_otros`. |
| `asset_type_raw` | Tipo tal como lo muestra la web (ej. `Bien 1 - Inmueble (Vivienda)`, `Piso`, `Finca Urbana`). |
| `status_code` | Estado unificado: `proxima`, `en_curso`, `adjudicacion_directa`, `cesion_remate`, `otro`. |
| `status` | Estado tal como lo da la web (ej. `Próxima apertura`, `Celebrándose`). |
| `province_code`, `province` | Provincia unificada: código INE y nombre canónico (`08`, `Barcelona`). |
| `province_raw` | Provincia tal como venía (`BARCELONA`, `ALACANT/ALICANTE`, `S.C. DE TENERIFE`…). |
| `locality` | Localidad. |
| `start_date`, `end_date` | Fechas `YYYY-MM-DD`. |
| `valuation_cents` | Valor de subasta / tipo de salida. |
| `appraisal_cents` | Tasación. |
| `min_bid_cents` | Puja mínima / precio. |
| `deposit_cents` | Depósito (`0` = no requiere depósito). |
| `claimed_cents` | Cantidad reclamada. |
| `charges_cents` | Cargas. |
| `cadastral_ref`, `description`, `main_residence` | Datos del bien. |
| `url` | Enlace a la subasta. |
| `extra` | JSON con campos propios de cada fuente (lote, dirección, tipo de subasta...). |
| `detailed_at` | Última vez que se descargó su detalle completo. |
| `first_seen`, `last_seen` | Primera y última ejecución en que apareció (UTC, RFC 3339). |
| `active` | `1` si apareció en la última ejecución de su fuente, `0` si ya no aparece. |

Los importes están en **céntimos de euro** (`INTEGER`). `NULL` significa que el dato no está disponible.

### `auctions_v`

Vista con las mismas columnas más los porcentajes calculados:

- `pct_claimed_valuation`: reclamado / valor de subasta × 100
- `pct_claimed_appraisal`: reclamado / tasación × 100
- `pct_deposit_valuation`: depósito / valor de subasta × 100

### `runs`

Historial de ejecuciones: `source`, `started_at`, `finished_at`, `status` (`running` / `ok` / `error`),
`items`, `new_items`, `error`, `triggered_by` (`cli` / `schedule` / `manual`) y `user_id` (quién lo
lanzó a mano).

### Tablas de la web

| Tabla | Contenido |
|---|---|
| `users` | Email, hash de la contraseña, rol (`admin` / `user`), activo. |
| `sessions` | Sesiones abiertas (hash del token, token CSRF, caducidad). |
| `jobs` | Horario (expresión cron) y si la ejecución automática está activa. |
| `filters` | Filtros guardados: criterios en JSON, modo de alerta (`none` / `on_run` / `daily`), hora, email, última alerta. |
| `alerts_sent` | Historial de emails enviados (o con error) por filtro. |

### Cómo se actualiza

1. Mientras se descarga, cada lote se inserta o actualiza (upsert) y se normaliza (provincia, estado,
   subtipo). `first_seen` se conserva y `last_seen` pasa a ser el inicio del run. Si un guardado no
   trae los campos de detalle, se conservan los que ya había.
2. Las subastas que siguen en el listado pero no hizo falta descargar solo actualizan `last_seen`.
3. Al terminar **bien**, las subastas de esa fuente que no aparecieron se marcan con `active = 0`.
   Si el job falla o no devuelve nada, no se retira ninguna.

La normalización vive en `internal/normalize` (catálogo de las 52 provincias con alias, palabras
clave de subtipos). Al migrar una base existente se recalcula para todos los registros.

### Consultas de ejemplo

```sql
-- resumen por fuente
SELECT source, count(*) total, sum(active) activas FROM auctions GROUP BY source;

-- subastas vistas por primera vez hoy (UTC)
SELECT source, external_id, locality, valuation_cents / 100.0 AS valor, url
FROM auctions WHERE date(first_seen) = date('now');

-- viviendas activas con reclamado < 50.000 € y < 33 % del valor de subasta
SELECT public_id, locality, province, claimed_cents / 100.0, round(pct_claimed_valuation, 1), url
FROM auctions_v
WHERE active = 1 AND asset_subtype = 'vivienda'
  AND claimed_cents < 5000000 AND pct_claimed_valuation < 33;

-- subastas activas por provincia y subtipo
SELECT province, asset_subtype, count(*) FROM auctions
WHERE active = 1 GROUP BY 1, 2 ORDER BY 1, 3 DESC;

-- últimas ejecuciones
SELECT * FROM runs ORDER BY id DESC LIMIT 10;
```

## Estructura del código

```
cmd/sboe/                 punto de entrada: subcomandos run, serve, createuser
internal/config/          configuración por variables de entorno (+ .env)
internal/httpx/           cliente HTTP común a todas las fuentes
internal/model/           struct Auction (modelo común)
internal/normalize/       parseo de importes, fechas, tipo de bien y texto
internal/sources/         interface Source + una carpeta por fuente
  boe/  procuradores/  segsocial/
internal/store/           SQLite: migraciones y upsert
internal/runner/          registro de jobs y Manager (un job a la vez, manual / programado)
internal/scheduler/       cron de jobs según la tabla jobs + tick por minuto (alertas diarias)
internal/filter/          criterios de filtro → SQL (los usan la web y las alertas)
internal/alerts/          envío de alertas (al terminar un job y diarias) + plantilla del email
internal/notify/          envío de emails (Resend o SMTP)
internal/auth/            contraseñas, sesiones, roles, CSRF
internal/web/             handlers HTTP, plantillas html/template y estáticos (htmx, CSS) embebidos
```

### Cliente HTTP (`internal/httpx`)

Todas las fuentes hacen sus peticiones con `httpx.Request` (`Method`, `URL`, `Query`, `Form`, `Headers`)
y `Client.Do`, que se encarga de:

- cabeceras de navegador por defecto,
- cookies por fuente (sesión `JSESSIONID` de la Seguridad Social),
- convertir la respuesta a UTF-8 según el charset (la Seguridad Social sirve ISO-8859-1),
- timeout y reintentos con backoff en errores de red, 429 y 5xx,
- `Pace()`: pausa aleatoria entre peticiones para no saturar las webs,
- volcado opcional del HTML (`-dump-html`).

### Añadir una fuente nueva

1. Crear `internal/sources/<nombre>/` con un tipo que implemente `sources.Source`:
   ```go
   type Source interface {
       Name() string
       Fetch(ctx context.Context, sink sources.Sink) error
   }
   ```
   `Fetch` entrega las subastas con `sink.Save(...)` a medida que las descarga, usa
   `sources.NeedsDetail(sink, id, estado)` para decidir si pedir el detalle y `sink.Touch(ids...)`
   para las que no cambiaron. Opcional: `Group() string` si comparte servidor con otra fuente.
2. Usar `httpx.Client` para las peticiones y `normalize` para importes y fechas. Provincia, estado y
   subtipo se unifican solos al guardar (`normalize.Enrich`), basta con rellenar los campos crudos.
3. Registrarla en `runner.Registry` (`internal/runner/runner.go`).
4. Añadir HTML real en `testdata/` y un test del parser.

## Tests

```sh
go test ./...
```

Hay tests de los parsers, la normalización, el store, los filtros (incluida la traducción a SQL), las
alertas (solo subastas nuevas; diaria una vez al día), los horarios y la web (login, roles, CSRF,
flujo de filtros). Los parsers se prueban contra HTML real guardado en `internal/sources/*/testdata/`. Si una web cambia
su HTML, captura páginas nuevas con `-dump-html`, reemplaza los fixtures y ajusta el parser.

## Notas y limitaciones

- **BOE**: el listado usa dos búsquedas guardadas (`id_busqueda`) copiadas de la versión TS. Si el BOE
  las invalida, hay que regenerarlas desde la búsqueda avanzada del portal
  (`internal/sources/boe/boe.go`). En subastas por lotes se guardan los importes del **lote 1**.
  Las subastas en "Próxima apertura" no traen fechas hasta que empiezan.
- **Procuradores**: algunas ubicaciones no indican la provincia, y en ese caso `province` queda vacío.
- **Seguridad Social**: la fecha que aparece en los títulos de las tablas es la de la consulta, no la
  de la subasta, así que se ignora. Algunos inmuebles en adjudicación no publican precio. En las
  subastas de embargos, las “Finca Urbana” solo traen la dirección en el listado, así que muchas quedan
  como `inmueble_otros` (el tipo exacto está en su página de detalle, que todavía no se descarga).
- Sin provincia: vehículos y bienes muebles del BOE no indican ubicación, y algunas localidades de
  Procuradores tampoco (p. ej. “Campello (el)”).

## Roadmap

- [x] Admin con usuarios para configurar los jobs (hora de ejecución, activar/desactivar, ejecución manual).
- [x] Filtros por usuario con alertas por email (al correr los jobs o diarias a una hora).
- [x] Guardado por lotes, modo incremental y fuentes en paralelo.
- [x] Campos unificados (provincia INE, estado, subtipo) e ID público.
- [ ] Detalle de las subastas de embargos de la Seguridad Social (tipo exacto de las “Finca Urbana”).
- [ ] Recuperación de contraseña por email.
