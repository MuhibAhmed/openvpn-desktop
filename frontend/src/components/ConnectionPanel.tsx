import { useEffect, useMemo, useRef, useState } from "react";

import type { Profile, Status } from "../types";
import { isBusy, isLive } from "../types";
import { bytes, duration, isZeroTime, rate, since } from "../format";
import { AlertIcon, ArrowDownIcon, ArrowUpIcon, PowerIcon } from "../icons";

type Props = {
  profile: Profile | null;
  status: Status;
  onConnect: () => void;
  onDisconnect: () => void;
};

/** headline is the large status word. */
function headline(status: Status, isThisProfile: boolean): string {
  if (!isThisProfile) return "Not connected";
  switch (status.phase) {
    case "connected":
      return "Protected";
    case "failed":
      return "Could not connect";
    case "idle":
      return "Not connected";
    case "reconnecting":
      return "Reconnecting";
    case "disconnecting":
      return "Disconnecting";
    default:
      return "Connecting";
  }
}

export function ConnectionPanel({
  profile,
  status,
  onConnect,
  onDisconnect,
}: Props) {
  // Only reflect live state when the status belongs to the selected profile;
  // otherwise selecting another profile would look like it is connecting.
  const isThisProfile = !!profile && status.profileId === profile.id;
  const phase = isThisProfile ? status.phase : "idle";

  const connected = phase === "connected";
  const busy = isBusy(phase);
  const failed = phase === "failed";

  const history = useThroughput(status, isThisProfile);
  const elapsed = useElapsed(
    isThisProfile && !isZeroTime(status.connectedAt) ? status.connectedAt : "",
  );

  const dialClass = [
    "dial",
    connected ? "dial--on" : "",
    busy ? "dial--busy" : "",
    failed ? "dial--bad" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className="stage">
      <div className={dialClass}>
        <div className="dial__ring" />
        {/* The glyph carries the action on its own. The word underneath was
            redundant: the headline below already says what the state is, and the
            ring and fill say whether pressing it will connect or disconnect. */}
        <button
          className="dial__button"
          disabled={!profile || phase === "disconnecting"}
          onClick={isLive(phase) ? onDisconnect : onConnect}
          aria-label={isLive(phase) ? "Disconnect" : "Connect"}
          title={isLive(phase) ? "Disconnect" : "Connect"}
        >
          <PowerIcon size={54} />
        </button>
      </div>

      <div className="headline">
        <div className="headline__status">{headline(status, isThisProfile)}</div>
        <div className="headline__detail">
          {isThisProfile ? status.detail : profile?.server || ""}
        </div>
        {connected && profile?.routesAllTraffic && (
          <div>
            <span className="pill pill--on">All traffic through the tunnel</span>
          </div>
        )}
      </div>

      {failed && status.error && (
        <div className="banner banner--bad">
          <AlertIcon className="banner__icon" />
          <div>
            <div className="banner__title">The connection did not start</div>
            <div className="selectable">{status.error}</div>
          </div>
        </div>
      )}

      {isThisProfile && status.stalled && !failed && (
        <div className="banner banner--warn">
          <AlertIcon className="banner__icon" />
          <div>
            <div className="banner__title">This is taking longer than it should</div>
            <div>
              openvpn has not reported anything for a while. The server may be
              unreachable, or it may be waiting on something. Open the connection
              log below to see the last thing it said, or disconnect and try
              again.
            </div>
          </div>
        </div>
      )}

      {isThisProfile && connected && (
        <>
          <div className="stats">
            <div className="stat">
              <div className="stat__label">Connected for</div>
              <div className="stat__value">{duration(elapsed)}</div>
            </div>
            <div className="stat">
              <div className="stat__label">
                <span className="inline">
                  <ArrowDownIcon /> Download
                </span>
              </div>
              <div className="stat__value">{bytes(status.bytesIn)}</div>
            </div>
            <div className="stat">
              <div className="stat__label">
                <span className="inline">
                  <ArrowUpIcon /> Upload
                </span>
              </div>
              <div className="stat__value">{bytes(status.bytesOut)}</div>
            </div>
          </div>

          <div className="card">
            <div className="card__head">
              <span>Throughput</span>
              <span className="small faint">
                {rate(history.lastIn)} in · {rate(history.lastOut)} out
              </span>
            </div>
            <div className="card__body">
              <Sparkline series={history.series} />
            </div>
          </div>
        </>
      )}

      {profile && (
        <div className="card">
          <div className="card__head">
            <span>Details</span>
          </div>
          <div className="card__body">
            <div className="detail-grid">
              <Detail label="Server" value={profile.server || "not set"} />
              <Detail
                label="Protocol"
                value={profile.protocol ? profile.protocol.toUpperCase() : "default"}
              />
              {isThisProfile && status.localIp && (
                <Detail label="Tunnel address" value={status.localIp} />
              )}
              {isThisProfile && status.remoteIp && (
                <Detail
                  label="Connected to"
                  value={
                    status.remotePort
                      ? `${status.remoteIp}:${status.remotePort}`
                      : status.remoteIp
                  }
                />
              )}
              <Detail
                label="Sign-in"
                value={
                  profile.needsCredentials
                    ? profile.hasSavedCredentials
                      ? "saved"
                      : "required"
                    : "certificate only"
                }
              />
            </div>
          </div>
        </div>
      )}

      {profile && profile.warnings && profile.warnings.length > 0 && (
        <div className="banner banner--warn">
          <AlertIcon className="banner__icon" />
          <div>
            <div className="banner__title">
              Worth checking in this profile
            </div>
            <ul style={{ margin: "4px 0 0", paddingLeft: 18 }}>
              {profile.warnings.map((w) => (
                <li key={w} className="selectable">
                  {w}
                </li>
              ))}
            </ul>
          </div>
        </div>
      )}
    </div>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="detail__label">{label}</div>
      <div className="detail__value">{value}</div>
    </div>
  );
}

/** samples kept in the throughput chart, one per byte-count report. */
const SPARK_POINTS = 48;

type Throughput = {
  series: { in: number; out: number }[];
  lastIn: number;
  lastOut: number;
};

/**
 * useThroughput turns cumulative byte counters into per-second rates.
 *
 * openvpn reports totals, not rates, and it reports them only when they change,
 * so intervals are uneven. Dividing by the actual elapsed time rather than
 * assuming one second keeps the chart honest.
 */
function useThroughput(status: Status, active: boolean): Throughput {
  const [series, setSeries] = useState<{ in: number; out: number }[]>([]);
  const previous = useRef<{ in: number; out: number; at: number } | null>(null);

  useEffect(() => {
    if (!active || status.phase !== "connected") {
      previous.current = null;
      setSeries([]);
      return;
    }

    const now = Date.now();
    const last = previous.current;
    previous.current = { in: status.bytesIn, out: status.bytesOut, at: now };

    if (!last) return;
    const seconds = (now - last.at) / 1000;
    if (seconds <= 0) return;

    // Counters reset when openvpn rebuilds the tunnel; a negative delta means
    // a new session rather than negative traffic.
    const deltaIn = Math.max(0, status.bytesIn - last.in);
    const deltaOut = Math.max(0, status.bytesOut - last.out);

    setSeries((current) =>
      [...current, { in: deltaIn / seconds, out: deltaOut / seconds }].slice(
        -SPARK_POINTS,
      ),
    );
  }, [status.bytesIn, status.bytesOut, status.phase, active]);

  const lastPoint = series[series.length - 1];
  return {
    series,
    lastIn: lastPoint?.in ?? 0,
    lastOut: lastPoint?.out ?? 0,
  };
}

/** useElapsed ticks once a second so the uptime counter advances. */
function useElapsed(connectedAt: string): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!connectedAt) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [connectedAt]);
  return connectedAt ? since(connectedAt, now) : 0;
}

/**
 * Sparkline draws download above the baseline and upload below it, so the two
 * directions are legible at a glance without a legend.
 */
function Sparkline({ series }: { series: { in: number; out: number }[] }) {
  const width = 560;
  const height = 34;
  const mid = height / 2;

  const paths = useMemo(() => {
    if (series.length < 2) return null;

    const peak = Math.max(
      1,
      ...series.map((p) => Math.max(p.in, p.out)),
    );
    const step = width / (series.length - 1);
    const scale = (v: number) => (v / peak) * (mid - 2);

    const line = (pick: (p: { in: number; out: number }) => number, up: boolean) =>
      series
        .map((p, i) => {
          const x = (i * step).toFixed(1);
          const y = (up ? mid - scale(pick(p)) : mid + scale(pick(p))).toFixed(1);
          return `${i === 0 ? "M" : "L"}${x},${y}`;
        })
        .join(" ");

    return {
      download: line((p) => p.in, true),
      upload: line((p) => p.out, false),
    };
  }, [series]);

  return (
    <svg
      className="spark"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      role="img"
      aria-label="Throughput over the last minute"
    >
      <line
        x1="0"
        y1={mid}
        x2={width}
        y2={mid}
        stroke="var(--border)"
        strokeWidth="1"
      />
      {paths ? (
        <>
          <path
            d={paths.download}
            fill="none"
            stroke="var(--accent)"
            strokeWidth="1.75"
            vectorEffect="non-scaling-stroke"
          />
          <path
            d={paths.upload}
            fill="none"
            stroke="var(--info)"
            strokeWidth="1.75"
            vectorEffect="non-scaling-stroke"
          />
        </>
      ) : (
        <text
          x={width / 2}
          y={mid + 4}
          textAnchor="middle"
          fill="var(--text-faint)"
          fontSize="11"
        >
          waiting for traffic
        </text>
      )}
    </svg>
  );
}
