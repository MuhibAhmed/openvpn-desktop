import { useEffect, useMemo, useRef, useState } from "react";

import type { LogLine } from "../types";
import { clockTime } from "../format";
import { ChevronIcon, FolderIcon } from "../icons";

type Props = {
  lines: LogLine[];
  open: boolean;
  onToggle: () => void;
  onOpenLogFolder: () => void;
};

/** noisy log levels the "problems only" filter keeps. */
const PROBLEM_LEVELS = new Set(["W", "F", "N"]);

export function LogDrawer({ lines, open, onToggle, onOpenLogFolder }: Props) {
  const [problemsOnly, setProblemsOnly] = useState(false);
  const body = useRef<HTMLDivElement>(null);
  const pinnedToBottom = useRef(true);

  const visible = useMemo(
    () =>
      problemsOnly
        ? lines.filter((l) => PROBLEM_LEVELS.has(l.level))
        : lines,
    [lines, problemsOnly],
  );

  // Follow the tail, but stop following the moment the user scrolls up to read
  // something -- yanking them back to the bottom is the worst part of most log
  // views.
  useEffect(() => {
    const el = body.current;
    if (!el || !open || !pinnedToBottom.current) return;
    el.scrollTop = el.scrollHeight;
  }, [visible.length, open]);

  const onScroll = () => {
    const el = body.current;
    if (!el) return;
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    pinnedToBottom.current = distanceFromBottom < 24;
  };

  return (
    <section className="logs">
      <div className="logs__bar">
        <button className="logs__title" onClick={onToggle}>
          <ChevronIcon
            size={14}
            className={open ? "chev chev--open" : "chev"}
          />
          Connection log
          {lines.length > 0 && (
            <span className="faint">({lines.length})</span>
          )}
        </button>

        <div className="logs__spacer" />

        {open && (
          <>
            <label className="check" style={{ padding: 0 }}>
              <input
                type="checkbox"
                checked={problemsOnly}
                onChange={(e) => setProblemsOnly(e.target.checked)}
              />
              <span className="check__text small">Problems only</span>
            </label>
            <button
              className="btn btn--ghost"
              onClick={() => copy(visible)}
              disabled={visible.length === 0}
            >
              Copy
            </button>
            <button className="btn btn--ghost" onClick={onOpenLogFolder}>
              <FolderIcon />
              Files
            </button>
          </>
        )}
      </div>

      {open && (
        <div className="logs__body" ref={body} onScroll={onScroll}>
          {visible.length === 0 ? (
            <div className="faint">
              {lines.length === 0
                ? "Nothing yet. Connect to see what openvpn reports."
                : "No warnings or errors in this session."}
            </div>
          ) : (
            visible.map((line, i) => (
              <div
                key={`${line.at}-${i}`}
                className={`logline logline--${line.level || "I"}`}
              >
                <span className="logline__time">{clockTime(line.at)}</span>
                <span className="logline__text">{line.text}</span>
              </div>
            ))
          )}
        </div>
      )}
    </section>
  );
}

/** copy puts the visible log on the clipboard, for pasting into a bug report. */
function copy(lines: LogLine[]) {
  const text = lines
    .map((l) => `${clockTime(l.at)} ${l.level || "I"} ${l.text}`)
    .join("\n");
  navigator.clipboard?.writeText(text).catch(() => {
    // Clipboard access can be refused; there is nothing useful to say about it.
  });
}
