import { useEffect, useRef } from "react";

/**
 * An interactive terminal (xterm.js) wired to a panel WebSocket endpoint.
 * Terminal input has to travel in both directions, so this is the one place
 * the UI uses a WebSocket.
 */
export function XTerminal({
  url,
  banner,
  className,
}: {
  url: string;
  banner?: string;
  className?: string;
}) {
  const host = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const hostEl = host.current;
    if (!hostEl) return;
    let disposed = false;
    let socket: WebSocket | undefined;
    let removeResizeListener: (() => void) | undefined;

    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([
        import("@xterm/xterm"),
        import("@xterm/addon-fit"),
      ]);
      if (disposed || !hostEl) return;

      const term = new Terminal({
        cursorBlink: true,
        fontSize: 12,
        fontFamily: "var(--font-mono)",
        theme: { background: "#0a0a0a", foreground: "#e5e5e5" },
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(hostEl);
      fit.fit();

      socket = new WebSocket(url);
      socket.binaryType = "arraybuffer";

      socket.addEventListener("open", () => {
        term.writeln(banner ?? "\r\nConnected.\r\n");
        // Sync the pty with the real terminal size right away; later changes
        // ride on term.onResize.
        socket?.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
      });
      socket.addEventListener("message", (event) => {
        if (event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data));
          return;
        }
        term.writeln(String(event.data));
      });
      socket.addEventListener("close", () => term.writeln("\r\nDisconnected."));

      term.onData((data) => {
        if (socket?.readyState === WebSocket.OPEN) socket.send(data);
      });
      term.onResize(({ rows, cols }) => {
        if (socket?.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: "resize", rows, cols }));
        }
      });

      const onResize = () => fit.fit();
      window.addEventListener("resize", onResize);
      removeResizeListener = () => window.removeEventListener("resize", onResize);
    })();

    return () => {
      disposed = true;
      removeResizeListener?.();
      socket?.close();
    };
  }, [url, banner]);

  return <div ref={host} className={className ?? "h-[24rem]"} />;
}
