# Design System

## Principles
Dockfly es un panel para quien opera su propia infraestructura. Debe sentirse **limpio, aireado y con energía**:
barra lateral con iconos, tarjetas blancas que flotan sobre un fondo gris azulado, cifras destacadas arriba y un
color de marca (oliva) que señala dónde actuar, con un acento arena para detalles. No debe parecer una plantilla gris genérica y plana.

Los tokens viven en `web/src/index.css` (variables CSS que Tailwind expone como `bg-primary`, `text-muted-foreground`…).
No se usan colores sueltos fuera de ellos, salvo los de estado indicados abajo.

## Colour
Neutros con un matiz oliva (tono 115, croma baja), oliva como color principal y arena como acento. Logo: libélula con alas de contenedores (`web/public/favicon.svg`).

| Token | Light | Dark | Uso |
|---|---|---|---|
| `--background` | `oklch(0.972 0.008 264)` | `oklch(0.155 0.015 265)` | Fondo de página |
| `--card` / `--popover` | `oklch(1 0 0)` | `oklch(0.2 0.018 265)` | Tarjetas, barra lateral, campos, diálogos |
| `--foreground` | `oklch(0.17 0.02 260)` | `oklch(0.97 0.005 255)` | Texto |
| `--muted-foreground` | `oklch(0.5 0.02 257)` | `oklch(0.72 0.02 257)` | Texto secundario (≥ 4.5:1 sobre `--muted`) |
| `--primary` | `oklch(0.488 0.088 124)` (#56682c) | `oklch(0.809 0.11 122)` (#b5cc7a) | Acción principal, enlace activo, foco |
| `--highlight` | `oklch(0.664 0.109 77)` (#b98a3e) | `oklch(0.842 0.067 80)` (#e2c79a) | Acento arena: gráficos, una cifra por página, detalles. Nunca botones ni texto sobre blanco (usar `--highlight-foreground`) |
| `--primary-foreground` | `oklch(0.985 0.005 255)` | `oklch(0.16 0.03 262)` | Texto sobre primario |
| `--muted` / `--secondary` | `oklch(0.955 0.012 264)` | `oklch(0.27 0.015 260)` | Superficies tonales, hover |
| `--destructive` | `oklch(0.52 0.22 27)` | `oklch(0.704 0.191 22.216)` | Borrar, errores |
| `--border` | `oklch(0.925 0.01 264)` | blanco 10 % | Divisores |
| `--input` | `oklch(0.87 0.014 264)` | blanco 16 % | Borde de campos (suave a propósito: el campo se distingue por su fondo blanco) |
| `--ring` | = `--primary` | = `--primary` | Anillo de foco |

Estado (siempre con texto, nunca solo color): correcto `emerald-500` en puntos y `text-emerald-700 dark:text-emerald-400`
en texto; aviso `amber-500` / `text-amber-700 dark:text-amber-400`; fallo `--destructive`.

## Typography
Geist Variable para todo; `font-mono` para valores técnicos (dominios internos, imágenes, puertos, ids).
Escala: 12 (`text-xs`, metadatos y cabeceras de tabla) · 14 (`text-sm`, cuerpo) · 16 (`text-base`, título de tarjeta) ·
18 (`text-lg`, título de tarjeta de sección) · 24 (`text-2xl`, título de página y cifras). Títulos en `font-semibold tracking-tight`; el resto 400/500.

## Spacing
Escala de Tailwind (4 px). Dentro de un grupo 4–8 px; entre campos 16 px; entre tarjetas 16–20 px; relleno de tarjeta 24 px.
Ancho máximo del contenido: `max-w-6xl`.

## Radius, shadow, border
- Sin radio: `--radius: 0`, todas las esquinas rectas (tarjetas, botones, campos, badges, interruptores, puntos de estado y logo). No usar `rounded-full` ni radios fijos.
- Profundidad por **sombra suave** (`shadow-card`), sin bordes en las tarjetas. En oscuro la sombra se sustituye por
  un contorno interior tenue. El botón primario y el enlace activo llevan sombra teñida de marca.

## Layout
- Escritorio (≥ lg): barra lateral fija de 16 rem con logo, navegación con iconos y el estado del sistema abajo.
- Por debajo de lg: logo y usuario arriba, navegación en una fila de píldoras desplazable.
- Cada página: `PageHeader` (título, descripción, acción principal) → `StatGrid` con cifras → contenido en tarjetas.

## Motion
100–150 ms, solo en hover/entrada de diálogos. Nada decorativo que retrase la tarea.

## Components
- **Un botón primario por vista** (el de crear, en `PageHeader`). El resto `outline` o `ghost`; borrar en `destructive`.
- **Cifras**: `Stat` dentro de `StatGrid` (icono en cuadro teñido, etiqueta pequeña, número grande). Máximo cuatro.
- **Elementos en rejilla** (apps, sitios): tarjeta flotante con icono en cuadro teñido, nombre y estado.
- **Carga y error de carga**: `ResourceState` con `useResource`. Una lista vacía y una carga fallida no se pintan igual.
- **Formularios en diálogo**: `FormDialog` (no se cierra con clic fuera ni mientras guarda).
- **Confirmaciones**: `useConfirm`, con `description` cuando la acción rompe algo más que el elemento borrado.
- **Botones de solo icono**: siempre con `aria-label` que nombre el elemento (`Delete ${name}`).
- **Estado vacío**: icono en círculo `bg-primary/10 text-primary`, título, una línea y la acción de crear.
- **Tablas**: cabecera pequeña en mayúsculas y atenuada; filas clicables llevan además un botón enfocable.

## Do / Don't
- Sí: oliva solo en la acción principal, navegación activa, enlaces y foco; arena con moderación.
- No: degradados de color, texto con degradado, colores de Tailwind sueltos para decorar.
- No: `text-*-600` para texto de estado en claro (no llega a 4.5:1); usar `-700`.
