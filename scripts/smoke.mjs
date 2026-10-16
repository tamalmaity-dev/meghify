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
  const text = await response.text();
  return `${response.status} ${text.slice(0, 200)}`;
}

// The panel may already have accounts from a previous run, in which case the
// bootstrap call answers 403 and the sign-in step below supplies the session.
const steps = [
  ["GET", "/api/health"],
  ["GET", "/api/auth/session"],
  ["POST", "/api/auth/bootstrap", { email: "admin@example.com", password: "supersecret123", name: "Admin" }],
  ["GET", "/api/auth/session"],
  ["POST", "/api/auth/login", { email: "admin@example.com", password: "supersecret123" }],
  ["GET", "/api/me"],
  ["POST", "/api/projects", { name: "Acme", description: "First project" }],
  ["GET", "/api/projects"],
  ["GET", "/api/stats"],
];

for (const [method, path, body] of steps) {
  console.log(method.padEnd(5), path.padEnd(28), await call(method, path, body));
}
