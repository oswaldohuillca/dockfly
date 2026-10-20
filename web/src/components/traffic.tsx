import { useEffect, useState } from "react";
import { Activity } from "lucide-react";

import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  api,
  type TrafficPoint,
  type TrafficRange,
  type TrafficTotals,
} from "@/lib/api";
import { cn } from "@/lib/utils";

const compact = new Intl.NumberFormat("es", {
  notation: "compact",
  maximumFractionDigits: 1,
});

export function formatMs(ms: number): string {
  if (ms <= 0) return "—";
  if (ms < 1) return "<1 ms";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
}

function errorRate(t: { requests: number; s5xx: number }) {
  return t.requests > 0 ? (t.s5xx / t.requests) * 100 : 0;
}

function pointLabel(p: TrafficPoint, range: TrafficRange) {
  const d = new Date(p.t);
  const time = d.toLocaleTimeString("es", {
    hour: "2-digit",
    minute: "2-digit",
  });
  return range === "7d"
    ? `${d.toLocaleDateString("es", { weekday: "short", day: "numeric" })} ${time}`
    : time;
}

/** Peticiones (barras por estado) y latencia p95 de un proxy host, sitio o app. */
export function TrafficCard({
  owner,
  bare,
}: {
  owner: string;
  bare?: boolean;
}) {
  const [range, setRange] = useState<TrafficRange>("24h");
  const [data, setData] = useState<{
    points: TrafficPoint[];
    totals: TrafficTotals;
  } | null>(null);

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api
        .traffic(owner, range)
        .then((d) => !cancelled && setData(d))
        .catch(() => {});
    load();
    const t = setInterval(load, 60_000);
    return () => {
      cancelled = true;
      clearInterval(t);
    };
  }, [owner, range]);

  const picker = (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      value={range}
      onValueChange={(v) => v && setRange(v as TrafficRange)}
    >
      <ToggleGroupItem value="1h">1 h</ToggleGroupItem>
      <ToggleGroupItem value="24h">24 h</ToggleGroupItem>
      <ToggleGroupItem value="7d">7 días</ToggleGroupItem>
    </ToggleGroup>
  );
  const body = (
    <>
      {!data ? (
        <div className="h-40 animate-pulse rounded-md bg-muted" />
      ) : (
        <>
          <dl className="grid grid-cols-2 gap-4 sm:grid-cols-4">
            <Stat
              label="Peticiones"
              value={compact.format(data.totals.requests)}
            />
            <Stat
              label="Errores 5xx"
              value={`${errorRate(data.totals).toFixed(errorRate(data.totals) < 10 ? 1 : 0)} %`}
              tone={
                errorRate(data.totals) >= 5
                  ? "bad"
                  : errorRate(data.totals) >= 1
                    ? "warn"
                    : undefined
              }
            />
            <Stat label="Latencia p50" value={formatMs(data.totals.p50_ms)} />
            <Stat label="Latencia p95" value={formatMs(data.totals.p95_ms)} />
          </dl>
          {data.totals.requests === 0 ? (
            <p className="py-8 text-center text-sm text-muted-foreground">
              Sin peticiones en este periodo. Los datos se recogen cada minuto.
            </p>
          ) : (
            <>
              <RequestsChart points={data.points} range={range} />
              <LatencyChart points={data.points} range={range} />
            </>
          )}
        </>
      )}
    </>
  );
  if (bare) {
    return (
      <div className="space-y-4">
        <div className="flex justify-end">{picker}</div>
        {body}
      </div>
    );
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Activity className="size-4 text-muted-foreground" /> Tráfico
        </CardTitle>
        <CardDescription>
          Peticiones que han pasado por Traefik, por estado, y su latencia.
        </CardDescription>
        <CardAction>{picker}</CardAction>
      </CardHeader>
      <CardContent className="space-y-4">{body}</CardContent>
    </Card>
  );
}

function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: string;
  tone?: "warn" | "bad";
}) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          "text-xl font-semibold tabular-nums",
          tone === "warn" && "text-amber-600 dark:text-amber-400",
          tone === "bad" && "text-destructive",
        )}
      >
        {value}
      </dd>
    </div>
  );
}

const W = 600;

function RequestsChart({
  points,
  range,
}: {
  points: TrafficPoint[];
  range: TrafficRange;
}) {
  const H = 120;
  const peak = Math.max(1, ...points.map((p) => p.requests));
  const bw = W / points.length;
  const y = (v: number) => (v / peak) * (H - 4);
  return (
    <figure className="space-y-1">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-32 w-full"
        preserveAspectRatio="none"
        role="img"
        aria-label="Peticiones por intervalo"
      >
        {points.map((p, i) => {
          const ok = p.requests - p.s4xx - p.s5xx;
          const x = i * bw + bw * 0.1;
          const w = bw * 0.8;
          let top = H;
          const seg = (v: number, cls: string) => {
            if (v <= 0) return null;
            const h = y(v);
            top -= h;
            return (
              <rect
                key={cls}
                x={x}
                y={top}
                width={w}
                height={h}
                className={cls}
              />
            );
          };
          return (
            <g key={p.t}>
              <title>
                {`${pointLabel(p, range)}: ${p.requests} peticiones` +
                  (p.s4xx ? `, ${p.s4xx} 4xx` : "") +
                  (p.s5xx ? `, ${p.s5xx} 5xx` : "")}
              </title>
              <rect
                x={i * bw}
                y={0}
                width={bw}
                height={H}
                className="fill-transparent hover:fill-muted"
              />
              {seg(ok, "fill-primary/70")}
              {seg(p.s4xx, "fill-amber-500")}
              {seg(p.s5xx, "fill-destructive")}
            </g>
          );
        })}
      </svg>
      <figcaption className="flex justify-between text-xs text-muted-foreground">
        <span>{pointLabel(points[0], range)}</span>
        <span className="flex gap-3">
          <Legend className="bg-primary/70">2xx/3xx</Legend>
          <Legend className="bg-amber-500">4xx</Legend>
          <Legend className="bg-destructive">5xx</Legend>
          <span>máx. {compact.format(peak)}</span>
        </span>
        <span>ahora</span>
      </figcaption>
    </figure>
  );
}

function Legend({
  className,
  children,
}: {
  className: string;
  children: React.ReactNode;
}) {
  return (
    <span className="flex items-center gap-1">
      <span className={cn("size-2 rounded-sm", className)} />
      {children}
    </span>
  );
}

function LatencyChart({
  points,
  range,
}: {
  points: TrafficPoint[];
  range: TrafficRange;
}) {
  const H = 60;
  const peak = Math.max(1, ...points.map((p) => p.p95_ms));
  const bw = W / points.length;
  const coords = points
    .map((p, i) =>
      p.requests > 0
        ? `${i * bw + bw / 2},${H - 2 - (p.p95_ms / peak) * (H - 6)}`
        : null,
    )
    .filter(Boolean)
    .join(" ");
  return (
    <figure className="space-y-1">
      <svg
        viewBox={`0 0 ${W} ${H}`}
        className="h-16 w-full"
        preserveAspectRatio="none"
        role="img"
        aria-label="Latencia p95"
      >
        <polyline
          points={coords}
          className="fill-none stroke-primary"
          strokeWidth={1.5}
          vectorEffect="non-scaling-stroke"
        />
        {points.map((p, i) =>
          p.requests > 0 ? (
            <line
              key={`d${p.t}`}
              x1={i * bw + bw / 2}
              x2={i * bw + bw / 2}
              y1={H - 2 - (p.p95_ms / peak) * (H - 6)}
              y2={H - 2 - (p.p95_ms / peak) * (H - 6)}
              className="stroke-primary"
              strokeWidth={4}
              strokeLinecap="round"
              vectorEffect="non-scaling-stroke"
            />
          ) : null,
        )}
        {points.map((p, i) => (
          <rect
            key={p.t}
            x={i * bw}
            y={0}
            width={bw}
            height={H}
            className="fill-transparent"
          >
            <title>{`${pointLabel(p, range)}: p95 ${formatMs(p.p95_ms)}, p50 ${formatMs(p.p50_ms)}`}</title>
          </rect>
        ))}
      </svg>
      <figcaption className="text-xs text-muted-foreground">
        Latencia p95 · máx. {formatMs(peak)}
      </figcaption>
    </figure>
  );
}

/** Minigráfica de las peticiones por hora de las últimas 24 h. */
export function Sparkline({
  values,
  className,
}: {
  values: number[];
  className?: string;
}) {
  const peak = Math.max(1, ...values);
  const w = 72;
  const h = 20;
  const step = w / Math.max(1, values.length - 1);
  const coords = values
    .map((v, i) => `${i * step},${h - 1 - (v / peak) * (h - 2)}`)
    .join(" ");
  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      className={cn("h-5 w-[72px]", className)}
      aria-hidden
    >
      <polyline
        points={coords}
        className="fill-none stroke-primary/70"
        strokeWidth={1.5}
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}

/** Resumen de tráfico de todas las rutas (se refresca cada minuto). */
export function useTrafficSummary() {
  const [summary, setSummary] = useState<
    Awaited<ReturnType<typeof api.trafficSummary>>
  >({});
  useEffect(() => {
    const load = () =>
      api
        .trafficSummary()
        .then(setSummary)
        .catch(() => {});
    load();
    const t = setInterval(load, 60_000);
    return () => clearInterval(t);
  }, []);
  return summary;
}

export { compact as compactNumber };
