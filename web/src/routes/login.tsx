import { useState } from "react";
import { TerminalSquare } from "lucide-react";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input, Label } from "@/components/ui/input";
import { useBootstrap, useLogin, useSession } from "@/lib/session";
import { createFileRoute } from "@tanstack/react-router";

type Mode = "login" | "bootstrap";

/**
 * One screen for both first-run setup and daily sign-in. When the instance has no
 * users the form becomes a bootstrap flow and creates the first admin.
 */
export function LoginPage() {
  const { version } = useSession();
  const navigate = useNavigate();
  const login = useLogin();
  const bootstrap = useBootstrap();

  const [mode, setMode] = useState<Mode>("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [code, setCode] = useState("");
  const [needsCode, setNeedsCode] = useState(false);
  const [error, setError] = useState("");

  const submit = async () => {
    setError("");
    try {
      if (mode === "bootstrap") {
        await bootstrap.mutateAsync({ email, password, name });
      } else {
        await login.mutateAsync({ email, password, code: code || undefined });
      }
      navigate({ to: "/" });
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      if (message.toLowerCase().includes("totp")) {
        setNeedsCode(true);
        setError("Enter the code from your authenticator app.");
        return;
      }
      setError(message);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <div className="mb-5 flex items-center gap-2">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-brand/15 text-brand">
            <TerminalSquare className="h-4 w-4" />
          </div>
          <div>
            <div className="text-[15px] font-semibold tracking-tight">Meghify</div>
            <div className="text-[11px] text-black/45 dark:text-white/40">
              {version ? `v${version}` : "self-hosted PaaS"}
            </div>
          </div>
        </div>

        <Card>
          <CardContent className="space-y-4 p-5">
            <div>
              <h1 className="text-[18px] font-semibold tracking-tight">
                {mode === "bootstrap" ? "Create the first admin" : "Sign in"}
              </h1>
              <p className="mt-1 text-[12px] text-black/50 dark:text-white/45">
                {mode === "bootstrap"
                  ? "This instance has no accounts yet. The first user becomes the owner."
                  : "Use an account with access to this panel."}
              </p>
            </div>

            {mode === "bootstrap" && (
              <div>
                <Label htmlFor="name">Name</Label>
                <Input
                  id="name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  placeholder="Ada Lovelace"
                />
              </div>
            )}

            <div>
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                value={email}
                onChange={(event) => setEmail(event.target.value)}
                placeholder="ada@example.com"
                autoFocus
              />
            </div>

            <div>
              <Label htmlFor="password">Password</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                onKeyDown={(event) => event.key === "Enter" && submit()}
              />
            </div>

            {needsCode && (
              <div>
                <Label htmlFor="code">Authenticator code</Label>
                <Input
                  id="code"
                  value={code}
                  onChange={(event) => setCode(event.target.value)}
                  placeholder="000000"
                  className="font-mono tracking-[0.3em]"
                  onKeyDown={(event) => event.key === "Enter" && submit()}
                />
              </div>
            )}

            {error && <p className="text-[11px] text-danger">{error}</p>}

            <Button
              variant="primary"
              className="w-full"
              disabled={!email || !password || (mode === "bootstrap" && !name)}
              onClick={submit}
            >
              {mode === "bootstrap" ? "Create admin account" : "Sign in"}
            </Button>

            <div className="text-center">
              <button
                className="text-[11px] text-black/45 hover:underline dark:text-white/40"
                onClick={async () => {
                  if (mode === "login") {
                    setMode("bootstrap");
                    return;
                  }
                  setMode("login");
                }}
              >
                {mode === "login"
                  ? "First run? Create the owner account."
                  : "Already bootstrapped? Sign in instead."}
              </button>
            </div>
          </CardContent>
        </Card>

        <p className="mt-4 text-center text-[10px] text-black/35 dark:text-white/30">
          The panel is in early development. Treat any instance holding your credentials
          as sensitive infrastructure.
        </p>
      </div>
    </div>
  );
}

export const Route = createFileRoute("/login")({
  component: LoginPage,
});
