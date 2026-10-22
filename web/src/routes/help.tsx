import { Link, createFileRoute } from "@tanstack/react-router";
import {
  ArrowRight,
  BookOpen,
  Check,
  Container as ContainerIcon,
  Globe2,
  Network,
  Server,
  Terminal,
  TriangleAlert,
} from "lucide-react";
import type { ReactNode } from "react";
import { PageHeader } from "@/components/app-shell";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

/** Inline code that stays readable against the card surface. */
function Code({ children }: { children: ReactNode }) {
  return (
    <code className="rounded bg-white/[0.06] px-1 py-0.5 font-mono text-[11px] text-black/80 dark:text-white/75">
      {children}
    </code>
  );
}

function PortFlowRow({
  source,
  target,
  note,
}: {
  source: string;
  target: string;
  note?: string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="min-w-[8.5rem] rounded border border-line/50 bg-white/[0.03] px-2 py-1 font-mono text-[11px]">
        {source}
      </span>
      <ArrowRight className="h-3.5 w-3.5 shrink-0 text-brand" />
      <span className="rounded border border-brand/20 bg-brand/[0.07] px-2 py-1 font-mono text-[11px] text-brand">
        {target}
      </span>
      {note && (
        <span className="text-[10.5px] text-black/40 dark:text-white/30">{note}</span>
      )}
    </div>
  );
}

/**
 * A single, static reference page. Networking terminology is the most common
 * source of support questions, so the explanation lives in the panel instead
 * of only in the repository docs.
 */
export function HelpPage() {
  return (
    <>
      <PageHeader
        title="Help & concepts"
        description="Short explanations for the parts of Meghify that are easy to mix up. Start with ports and networking — that is where most Bad Gateway errors come from."
      />

      <div className="grid gap-3 xl:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="space-y-3">
          <Card id="ports">
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Network className="h-4 w-4 text-brand" />
                Ports & networking
              </CardTitle>
              <Badge>Start here</Badge>
            </CardHeader>
            <CardContent className="space-y-5">
              <p className="max-w-3xl text-[12.5px] leading-6 text-black/60 dark:text-white/50">
                A <strong className="font-medium text-current">container port</strong> belongs
                to one application. A <strong className="font-medium text-current">host
                port</strong> belongs to the VPS. They are different things even when the
                number is the same.
              </p>

              <div className="grid gap-3 sm:grid-cols-2">
                <div className="rounded-md border border-line/50 bg-white/[0.025] p-3.5">
                  <div className="flex items-center gap-2 text-[11px] text-black/45 dark:text-white/40">
                    <ContainerIcon className="h-3.5 w-3.5" />
                    Inside the application
                  </div>
                  <div className="mt-2 font-mono text-[22px] font-semibold tracking-tight text-brand">
                    3000
                  </div>
                  <div className="mt-1 text-[12.5px] font-medium">Container port</div>
                  <p className="mt-1 text-[11.5px] leading-5 text-black/50 dark:text-white/40">
                    The port the process inside the container listens on. Every application
                    has its own network namespace, so <Code>app-a:3000</Code> and{" "}
                    <Code>app-b:3000</Code> do not collide.
                  </p>
                </div>

                <div className="rounded-md border border-line/50 bg-white/[0.025] p-3.5">
                  <div className="flex items-center gap-2 text-[11px] text-black/45 dark:text-white/40">
                    <Server className="h-3.5 w-3.5" />
                    On the VPS
                  </div>
                  <div className="mt-2 font-mono text-[22px] font-semibold tracking-tight">
                    8001
                  </div>
                  <div className="mt-1 text-[12.5px] font-medium">Host / VPS port</div>
                  <p className="mt-1 text-[11.5px] leading-5 text-black/50 dark:text-white/40">
                    A port published by Docker on the machine itself. Host ports are global,
                    so two containers cannot publish the same host port at the same time.
                  </p>
                </div>
              </div>

              <div className="rounded-md border border-brand/20 bg-brand/[0.05] px-3.5 py-3">
                <div className="flex items-start gap-2.5">
                  <Check className="mt-0.5 h-4 w-4 shrink-0 text-brand" />
                  <p className="text-[12px] leading-5 text-black/65 dark:text-white/55">
                    <strong className="font-medium text-current">The rule:</strong> container
                    ports are per app; host ports are global. Two applications can both use
                    container port <Code>3000</Code> without any conflict.
                  </p>
                </div>
              </div>

              <section className="space-y-2">
                <h3 className="flex items-center gap-1.5 text-[12.5px] font-semibold">
                  <Globe2 className="h-3.5 w-3.5 text-brand" />
                  Domain routing — recommended
                </h3>
                <p className="text-[11.5px] leading-5 text-black/50 dark:text-white/40">
                  Traefik listens on the VPS and forwards requests to the application over
                  the private Docker network. No host port needs to be published.
                </p>
                <div className="overflow-x-auto rounded-md border border-line/50 bg-black/[0.12] p-3 dark:bg-black/20">
                  <div className="min-w-[32rem] space-y-2">
                    <PortFlowRow source="Internet :80 / :443" target="Traefik" />
                    <PortFlowRow source="Traefik" target="app-a:3000" note="domain A" />
                    <PortFlowRow source="Traefik" target="app-b:3000" note="domain B" />
                  </div>
                </div>
                <p className="text-[10.5px] leading-5 text-black/40 dark:text-white/30">
                  Both apps can listen on 3000. The network name in front of the port is what
                  keeps them apart.
                </p>
              </section>

              <section className="space-y-2">
                <h3 className="flex items-center gap-1.5 text-[12.5px] font-semibold">
                  <Server className="h-3.5 w-3.5 text-brand" />
                  Direct VPS access — only if you need it
                </h3>
                <p className="text-[11.5px] leading-5 text-black/50 dark:text-white/40">
                  A port mapping publishes a container port on the VPS. Only the host side
                  needs to be unique; the container side can stay the same.
                </p>
                <div className="overflow-x-auto rounded-md border border-line/50 bg-black/[0.12] p-3 dark:bg-black/20">
                  <div className="min-w-[32rem] space-y-2">
                    <PortFlowRow source="VPS :8001" target="app-a:3000" />
                    <PortFlowRow source="VPS :8002" target="app-b:3000" />
                  </div>
                </div>
                <p className="text-[10.5px] leading-5 text-black/40 dark:text-white/30">
                  With a domain, leave <strong className="font-medium">Port mappings</strong>{" "}
                  empty. Publish ports only for direct IP access or another service that
                  needs to connect from outside Docker.
                </p>
              </section>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <BookOpen className="h-4 w-4 text-brand" />
                What goes in each field
              </CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              <div className="overflow-x-auto">
                <table className="w-full min-w-[34rem] text-left text-[11.5px]">
                  <thead className="border-b border-line/50 text-[10.5px] uppercase tracking-wide text-black/40 dark:text-white/30">
                    <tr>
                      <th className="px-4 py-2.5 font-medium">Field</th>
                      <th className="px-4 py-2.5 font-medium">Means</th>
                      <th className="px-4 py-2.5 font-medium">Typical value</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-line/40">
                    <tr>
                      <td className="px-4 py-3 font-medium">Networking → Container port</td>
                      <td className="px-4 py-3 text-black/50 dark:text-white/40">
                        Port the app process listens on inside the container.
                      </td>
                      <td className="px-4 py-3 font-mono text-brand">3000</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-3 font-medium">Networking → Port mappings</td>
                      <td className="px-4 py-3 text-black/50 dark:text-white/40">
                        Optional <Code>hostPort:containerPort</Code>. For direct VPS access.
                      </td>
                      <td className="px-4 py-3 font-mono text-brand">8001:3000</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-3 font-medium">Domains → Internal port</td>
                      <td className="px-4 py-3 text-black/50 dark:text-white/40">
                        Per-domain override. Blank inherits the container port.
                      </td>
                      <td className="px-4 py-3 font-mono text-brand">blank</td>
                    </tr>
                    <tr>
                      <td className="px-4 py-3 font-medium">Environment → PORT</td>
                      <td className="px-4 py-3 text-black/50 dark:text-white/40">
                        Meghify passes the container port as <Code>PORT</Code> unless the app
                        defines it itself.
                      </td>
                      <td className="px-4 py-3 font-mono text-brand">automatic</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <TriangleAlert className="h-4 w-4 text-warn" />
                Fixing a Bad Gateway
              </CardTitle>
            </CardHeader>
            <CardContent>
              <p className="mb-3 text-[11.5px] leading-5 text-black/50 dark:text-white/40">
                <Code>502 Bad Gateway</Code> means Traefik reached the container but the
                application did not answer on the port the route targets. Work through
                these checks in order:
              </p>
              <ol className="space-y-2.5">
                {[
                  <>
                    Open <strong className="font-medium">Runtime logs</strong> and find the
                    actual listening port. A healthy app usually prints{" "}
                    <Code>listening on 0.0.0.0:3000</Code>.
                  </>,
                  <>
                    Set <strong className="font-medium">Networking → Container port</strong>{" "}
                    to that number. Do not change it just because another app uses the same
                    number.
                  </>,
                  <>
                    Make sure the app binds <Code>0.0.0.0</Code>, not only{" "}
                    <Code>127.0.0.1</Code>. A localhost-only listener is unreachable from
                    Traefik.
                  </>,
                  <>
                    If the app has an explicit <Code>PORT</Code> environment variable, remove
                    it or set it to the same value as the container port.
                  </>,
                  <>
                    Clear <strong className="font-medium">Domains → Internal port</strong> so
                    the domain follows the application port, then redeploy.
                  </>,
                ].map((step, index) => (
                  <li key={index} className="flex gap-2.5">
                    <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-brand/10 text-[10px] font-semibold text-brand">
                      {index + 1}
                    </span>
                    <span className="text-[11.5px] leading-5 text-black/60 dark:text-white/50">
                      {step}
                    </span>
                  </li>
                ))}
              </ol>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <ContainerIcon className="h-4 w-4 text-brand" />
                Two apps, both on 3000
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-[11.5px] leading-5 text-black/50 dark:text-white/40">
                This is a valid setup and the recommended default. It does not matter that
                another application is already using 3000 on the VPS.
              </p>
              <div className="grid gap-2 sm:grid-cols-2">
                <div className="rounded-md border border-line/50 bg-white/[0.025] p-3">
                  <div className="text-[12px] font-medium">App A — my-api</div>
                  <div className="mt-1.5 space-y-1 font-mono text-[10.5px] text-black/50 dark:text-white/40">
                    <div>container port: 3000</div>
                    <div>host mapping: none</div>
                    <div className="text-brand">domain: api.example.com</div>
                  </div>
                </div>
                <div className="rounded-md border border-line/50 bg-white/[0.025] p-3">
                  <div className="text-[12px] font-medium">App B — my-web</div>
                  <div className="mt-1.5 space-y-1 font-mono text-[10.5px] text-black/50 dark:text-white/40">
                    <div>container port: 3000</div>
                    <div>host mapping: none</div>
                    <div className="text-brand">domain: www.example.com</div>
                  </div>
                </div>
              </div>
              <p className="text-[10.5px] leading-5 text-black/40 dark:text-white/30">
                Only if you want direct VPS access do you need different host ports:
                use <Code>8001:3000</Code> for App A and <Code>8002:3000</Code> for App B.
              </p>
            </CardContent>
          </Card>
        </div>

        <aside className="space-y-3">
          <Card>
            <CardHeader>
              <CardTitle>Quick answers</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              {[
                {
                  question: "Do two apps need different container ports?",
                  answer: "No. Container ports are isolated per app.",
                },
                {
                  question: "Do I need a port mapping for a domain?",
                  answer: "No. Traefik reaches the app over the private network.",
                },
                {
                  question: "Which port do I put in Domains?",
                  answer: "Leave it blank to follow the container port.",
                },
                {
                  question: "Why did changing 3000 to 3001 cause 502?",
                  answer: "The route moved to 3001, but the app still listened on 3000.",
                },
              ].map((item) => (
                <div key={item.question} className="border-b border-line/40 pb-3 last:border-0 last:pb-0">
                  <div className="text-[11.5px] font-medium">{item.question}</div>
                  <p className="mt-0.5 text-[10.5px] leading-5 text-black/45 dark:text-white/35">
                    {item.answer}
                  </p>
                </div>
              ))}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <Terminal className="h-3.5 w-3.5 text-brand" />
                Check from the VPS
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <p className="text-[10.5px] leading-5 text-black/45 dark:text-white/35">
                These show what Docker published and what the host is already using.
              </p>
              <pre className="overflow-x-auto rounded-md border border-line/50 bg-black/[0.18] p-2.5 font-mono text-[10.5px] leading-5 text-black/65 dark:text-white/55">
                {`docker ps --format \\
  'table {{.Names}}\\t{{.Ports}}'

ss -ltnp | grep -E \\
  ':3000|:3001|:8000|:8080'`}
              </pre>
              <p className="text-[10.5px] leading-5 text-black/45 dark:text-white/35">
                If a host port is already taken, choose another host port for the mapping.
                The application's internal port can stay the same.
              </p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Where to find it</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2.5 text-[11.5px]">
              <Link
                to="/projects"
                className="flex items-center justify-between gap-2 rounded-md border border-line/50 px-2.5 py-2 transition-colors hover:bg-white/[0.03]"
              >
                Projects
                <ArrowRight className="h-3.5 w-3.5 text-black/35 dark:text-white/30" />
              </Link>
              <p className="text-[10.5px] leading-5 text-black/45 dark:text-white/35">
                Open an application and use <strong className="font-medium">Networking</strong>{" "}
                for the container port and mappings, or{" "}
                <strong className="font-medium">Runtime logs</strong> for its actual listener.
              </p>
            </CardContent>
          </Card>
        </aside>
      </div>
    </>
  );
}

export const Route = createFileRoute("/help")({
  component: HelpPage,
});
