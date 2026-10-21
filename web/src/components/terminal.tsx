import { useEffect, useRef, useState } from "react";
import type { ITheme } from "@xterm/xterm";
import { Minus, Plus, RotateCw } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * An interactive terminal (xterm.js) wired to a panel WebSocket endpoint.
 * Terminal input has to travel in both directions, so this is the one place
 * the UI uses a WebSocket.
 *
 * The server pings an idle WebSocket every 20 seconds; the client also
 * reconnects with backoff if a proxy or laptop sleep drops it anyway. Font
 * size and colour theme are per-browser preferences, persisted locally and
 * shared by every terminal in the panel.
 */

const PREFS_KEY = "meghify.terminal.prefs";
const MIN_FONT = 10;
const MAX_FONT = 22;

type TerminalPrefs = { theme: string; fontSize: number };

const THEMES: Record<string, { label: string; theme: ITheme }> = {
  midnight: {
    label: "Midnight",
    theme: {
      background: "#0b0b10",
      foreground: "#e6e6ec",
      cursor: "#c4b5fd",
      cursorAccent: "#0b0b10",
      selectionBackground: "#4c3b7a",
      black: "#17171f",
      red: "#f07178",
      green: "#7fd88f",
      yellow: "#ffcb6b",
      blue: "#82aaff",
      magenta: "#c792ea",
      cyan: "#89ddff",
      white: "#d6d6df",
      brightBlack: "#5c5c68",
      brightRed: "#ff8b92",
      brightGreen: "#a5e8b0",
      brightYellow: "#ffe08a",
      brightBlue: "#a6c1ff",
      brightMagenta: "#ddb6ff",
      brightCyan: "#b4eaff",
      brightWhite: "#ffffff",
    },
  },
  graphite: {
    label: "Graphite",
    theme: {
      background: "#18181b",
      foreground: "#e4e4e7",
      cursor: "#a1a1aa",
      cursorAccent: "#18181b",
      selectionBackground: "#3f3f46",
      black: "#27272a",
      red: "#fca5a5",
      green: "#86efac",
      yellow: "#fde68a",
      blue: "#93c5fd",
      magenta: "#d8b4fe",
      cyan: "#a5f3fc",
      white: "#e4e4e7",
      brightBlack: "#71717a",
      brightRed: "#fecaca",
      brightGreen: "#bbf7d0",
      brightYellow: "#fef3c7",
      brightBlue: "#bfdbfe",
      brightMagenta: "#e9d5ff",
      brightCyan: "#cffafe",
      brightWhite: "#fafafa",
    },
  },
  solarized: {
    label: "Solarized",
    theme: {
      background: "#002b36",
      foreground: "#93a1a1",
      cursor: "#b58900",
      cursorAccent: "#002b36",
      selectionBackground: "#073642",
      black: "#073642",
      red: "#dc322f",
      green: "#859900",
      yellow: "#b58900",
      blue: "#268bd2",
      magenta: "#d33682",
      cyan: "#2aa198",
      white: "#eee8d5",
      brightBlack: "#586e75",
      brightRed: "#cb4b16",
      brightGreen: "#93a1a1",
      brightYellow: "#fdf6e3",
      brightBlue: "#839496",
      brightMagenta: "#6c71c4",
      brightCyan: "#93a1a1",
      brightWhite: "#fdf6e3",
    },
  },
  dracula: {
    label: "Dracula",
    theme: {
      background: "#282a36",
      foreground: "#f8f8f2",
      cursor: "#f8f8f2",
      cursorAccent: "#282a36",
      selectionBackground: "#44475a",
      black: "#21222c",
      red: "#ff5555",
      green: "#50fa7b",
      yellow: "#f1fa8c",
      blue: "#bd93f9",
      magenta: "#ff79c6",
      cyan: "#8be9fd",
      white: "#f8f8f2",
      brightBlack: "#6272a4",
      brightRed: "#ff6e6e",
      brightGreen: "#69ff94",
      brightYellow: "#ffffa5",
      brightBlue: "#d6acff",
      brightMagenta: "#ff92df",
      brightCyan: "#a4ffff",
      brightWhite: "#ffffff",
    },
  },
  paper: {
    label: "Paper",
    theme: {
      background: "#f7f4ed",
      foreground: "#3f3a33",
      cursor: "#7c5cbf",
      cursorAccent: "#f7f4ed",
      selectionBackground: "#d9d0f0",
      black: "#3f3a33",
      red: "#b33939",
      green: "#307b52",
      yellow: "#946b00",
      blue: "#3566a8",
      magenta: "#8b4a8f",
      cyan: "#1f7a7a",
      white: "#e7e1d6",
      brightBlack: "#7a746a",
      brightRed: "#cf5b5b",
      brightGreen: "#4a9b6d",
      brightYellow: "#b38a24",
      brightBlue: "#5385c6",
      brightMagenta: "#a96bad",
      brightCyan: "#3c9999",
      brightWhite: "#ffffff",
    },
  },
};

function loadPrefs(): TerminalPrefs {
  const fallback: TerminalPrefs = { theme: "midnight", fontSize: 13 };
  try {
    const raw = localStorage.getItem(PREFS_KEY);
    if (!raw) return fallback;
    const parsed = JSON.parse(raw) as Partial<TerminalPrefs>;
    return {
      theme: parsed.theme && THEMES[parsed.theme] ? parsed.theme : fallback.theme,
      fontSize:
        typeof parsed.fontSize === "number"
          ? Math.min(MAX_FONT, Math.max(MIN_FONT, parsed.fontSize))
          : fallback.fontSize,
    };
  } catch {
    return fallback;
  }
}

type ConnectionState = "connecting" | "connected" | "reconnecting" | "disconnected";

const STATUS_TEXT: Record<ConnectionState, string> = {
  connecting: "Connecting",
  connected: "Connected",
  reconnecting: "Reconnecting",
  disconnected: "Disconnected",
};

const STATUS_DOT: Record<ConnectionState, string> = {
  connecting: "bg-white/40",
  connected: "bg-ok",
  reconnecting: "animate-pulse bg-warn",
  disconnected: "bg-danger",
};

export function XTerminal({
  url,
  banner,
  title,
  subtitle,
  className,
  toolbar = true,
}: {
  url: string;
  banner?: string;
  title?: string;
  subtitle?: string;
  className?: string;
  toolbar?: boolean;
}) {
  const host = useRef<HTMLDivElement>(null);
  const termRef = useRef<{
    term: import("@xterm/xterm").Terminal;
    fit: import("@xterm/addon-fit").FitAddon;
  } | null>(null);
  const connectRef = useRef<(() => void) | null>(null);
  const prefsRef = useRef<TerminalPrefs | null>(null);
  const [prefs, setPrefs] = useState<TerminalPrefs>(loadPrefs);
  const [status, setStatus] = useState<ConnectionState>("connecting");
  const theme = THEMES[prefs.theme] ?? THEMES.midnight;

  useEffect(() => {
    prefsRef.current = prefs;
    try {
      localStorage.setItem(PREFS_KEY, JSON.stringify(prefs));
    } catch {
      // Private-mode storage quotas must not break the terminal.
    }
  }, [prefs]);

  useEffect(() => {
    const hostEl = host.current;
    if (!hostEl) return;
    let disposed = false;
    let socket: WebSocket | undefined;
    let retryTimer: number | undefined;
    let stableTimer: number | undefined;
    let retries = 0;
    let intentionalClose = false;
    let removeResizeListener: (() => void) | undefined;

    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (disposed || !hostEl) return;

      const initialPrefs = prefsRef.current ?? { theme: "midnight", fontSize: 13 };
      const initialTheme = THEMES[initialPrefs.theme] ?? THEMES.midnight;
      const term = new Terminal({
        cursorBlink: true,
        fontSize: initialPrefs.fontSize,
        fontFamily: "var(--font-mono)",
        theme: initialTheme.theme,
        scrollback: 5000,
        allowProposedApi: true,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(hostEl);
      fit.fit();
      termRef.current = { term, fit };

      const sendResize = () => {
        if (socket?.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
        }
      };

      const scheduleReconnect = () => {
        if (disposed || intentionalClose) return;
        if (retries >= 5) {
          setStatus("disconnected");
          term.writeln("\r\n\x1b[31mConnection lost.\x1b[0m Reconnect from the toolbar above.");
          return;
        }
        retries += 1;
        setStatus("reconnecting");
        const delay = Math.min(1000 * 2 ** (retries - 1), 15000);
        term.writeln(
          `\r\n\x1b[33mConnection lost. Reconnecting in ${Math.round(delay / 1000)}s…\x1b[0m`,
        );
        retryTimer = window.setTimeout(connect, delay);
      };

      const connect = () => {
        if (disposed) return;
        window.clearTimeout(retryTimer);
        window.clearTimeout(stableTimer);
        if (socket) {
          socket.onclose = null;
          socket.close();
        }
        setStatus(retries > 0 ? "reconnecting" : "connecting");
        const ws = new WebSocket(url);
        socket = ws;
        ws.binaryType = "arraybuffer";

        ws.addEventListener("open", () => {
          if (disposed || socket !== ws) return;
          const reconnected = retries > 0;
          setStatus("connected");
          term.writeln(
            reconnected
              ? "\r\n\x1b[32mReconnected.\x1b[0m\r\n"
              : (banner ?? "\r\nConnected.\r\n"),
          );
          // Only call the connection healthy after it has survived a few
          // seconds. Otherwise a server that accepts and immediately closes
          // (for example a container without /bin/sh) would reconnect in a
          // tight loop because every open reset the backoff.
          window.clearTimeout(stableTimer);
          stableTimer = window.setTimeout(() => {
            retries = 0;
          }, 5000);
          requestAnimationFrame(() => {
            fit.fit();
            sendResize();
          });
        });
        ws.addEventListener("message", (event) => {
          if (disposed) return;
          if (event.data instanceof ArrayBuffer) {
            term.write(new Uint8Array(event.data));
            return;
          }
          term.writeln(String(event.data));
        });
        ws.addEventListener("close", () => {
          if (socket !== ws) return;
          // A connection that survived most of the healthy window still
          // counts as unstable; otherwise a flap every few seconds would
          // reset the backoff forever.
          window.clearTimeout(stableTimer);
          scheduleReconnect();
        });
      };

      connectRef.current = () => {
        if (disposed) return;
        retries = 0;
        connect();
      };

      term.onData((data) => {
        if (socket?.readyState === WebSocket.OPEN) socket.send(data);
      });
      term.onResize(() => sendResize());

      const onResize = () => fit.fit();
      window.addEventListener("resize", onResize);
      removeResizeListener = () => window.removeEventListener("resize", onResize);

      connect();
    })();

    return () => {
      disposed = true;
      intentionalClose = true;
      window.clearTimeout(retryTimer);
      window.clearTimeout(stableTimer);
      removeResizeListener?.();
      connectRef.current = null;
      termRef.current = null;
      if (socket) {
        socket.onclose = null;
        socket.close();
      }
    };
    // The theme and font-size effects below update the live terminal without
    // tearing down the WebSocket.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [url, banner]);

  useEffect(() => {
    const live = termRef.current;
    if (!live) return;
    live.term.options.fontSize = prefs.fontSize;
    live.term.options.theme = theme.theme;
    live.fit.fit();
    live.term.refresh(0, Math.max(0, live.term.rows - 1));
  }, [prefs.fontSize, theme]);

  const changeFont = (delta: number) => {
    setPrefs((current) => ({
      ...current,
      fontSize: Math.min(MAX_FONT, Math.max(MIN_FONT, current.fontSize + delta)),
    }));
  };

  return (
    <div
      className={cn("flex min-h-0 flex-col overflow-hidden bg-[#0b0b10]", className ?? "h-[24rem]")}
      style={{ backgroundColor: theme.theme.background }}
    >
      {toolbar && (
        <div className="flex h-9 shrink-0 items-center gap-2 border-b border-white/10 bg-[#141419] px-2.5 text-[11px] text-white/70">
          <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", STATUS_DOT[status])} />
          <span className="font-medium text-white/80">{title ?? "Terminal"}</span>
          {subtitle && (
            <span className="hidden min-w-0 truncate text-white/35 sm:inline">{subtitle}</span>
          )}
          <span className="hidden text-[10px] uppercase tracking-wide text-white/30 md:inline">
            {STATUS_TEXT[status]}
          </span>
          <div className="ml-auto flex items-center gap-1">
            <button
              type="button"
              onClick={() => changeFont(-1)}
              disabled={prefs.fontSize <= MIN_FONT}
              className="flex h-6 w-6 items-center justify-center rounded text-white/60 transition hover:bg-white/10 hover:text-white disabled:opacity-30"
              title="Decrease font size"
              aria-label="Decrease terminal font size"
            >
              <Minus className="h-3 w-3" />
            </button>
            <span className="w-7 text-center text-[10px] tabular-nums text-white/40">
              {prefs.fontSize}px
            </span>
            <button
              type="button"
              onClick={() => changeFont(1)}
              disabled={prefs.fontSize >= MAX_FONT}
              className="flex h-6 w-6 items-center justify-center rounded text-white/60 transition hover:bg-white/10 hover:text-white disabled:opacity-30"
              title="Increase font size"
              aria-label="Increase terminal font size"
            >
              <Plus className="h-3 w-3" />
            </button>
            <span className="mx-1 h-4 w-px bg-white/10" />
            <select
              value={prefs.theme}
              onChange={(event) =>
                setPrefs((current) => ({ ...current, theme: event.target.value }))
              }
              className="h-6 rounded border border-white/10 bg-black/30 px-1.5 text-[10px] text-white/70 outline-none focus:border-white/30"
              aria-label="Terminal theme"
              title="Terminal theme"
            >
              {Object.entries(THEMES).map(([key, value]) => (
                <option key={key} value={key} className="bg-[#18181b] text-white">
                  {value.label}
                </option>
              ))}
            </select>
            <button
              type="button"
              onClick={() => connectRef.current?.()}
              disabled={status === "connected" || status === "connecting"}
              className="ml-1 flex h-6 items-center gap-1 rounded px-1.5 text-[10px] text-white/55 transition hover:bg-white/10 hover:text-white disabled:opacity-30"
              title="Reconnect terminal"
            >
              <RotateCw className="h-3 w-3" />
              Reconnect
            </button>
          </div>
        </div>
      )}
      <div ref={host} className="min-h-0 flex-1" />
    </div>
  );
}
