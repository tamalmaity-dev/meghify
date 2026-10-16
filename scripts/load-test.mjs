const base = "http://127.0.0.1:8080";
let cookie = "";

async function call(method, path, body) {
  const headers = { Accept: "application/json" };
  if (cookie) headers["Cookie"] = cookie;
  if (body) headers["Content-Type"] = "application/json";
  const response = await fetch(base + path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
    redirect: "manual",
  });
  const setCookie = response.headers.get("set-cookie");
  if (setCookie) cookie = setCookie.split(";")[0];
  return { status: response.status, text: await response.text() };
}

// The panel already has accounts; sign in as the one the smoke test created.
const login = await call("POST", "/api/auth/login", {
  email: "admin@example.com",
  password: "supersecret123",
});
if (login.status !== 200) {
  // Otherwise bootstrap it here.
  const boot = await call("POST", "/api/auth/bootstrap", {
    email: "load@example.com",
    password: "supersecret123",
    name: "Load Test",
  });
  if (boot.status !== 201) {
    console.error("login:", login.status, login.text);
    console.error("bootstrap:", boot.status, boot.text);
    process.exit(1);
  }
}

const project = await call("POST", "/api/projects", { name: "Load", description: "load test" });
const projectId = JSON.parse(project.text).id;
const envs = await call("GET", `/api/projects/${projectId}/environments`);
const environmentId = JSON.parse(envs.text)[0].id;

for (let i = 0; i < 25; i++) {
  await call("POST", "/api/applications", {
    name: `svc-${i}`,
    environmentId,
    gitRepository: "https://example.com/app.git",
    gitBranch: "main",
    buildPack: "dockerfile",
    port: 3000,
  });
}

for (let round = 0; round < 40; round++) {
  await Promise.all([
    call("GET", "/api/projects"),
    call("GET", "/api/applications"),
    call("GET", "/api/stats"),
    call("GET", "/api/jobs?status="),
    call("GET", "/api/search?q=svc"),
  ]);
}

console.log("created applications and ran 200 requests");
