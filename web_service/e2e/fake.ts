// The test stack's fake OpenAI server (contract_tests/fake_openai/server.py), reached with docker exec as the
// contract tests do: script a model's next replies, read what it was sent. Scripts are per model name, so each spec
// uses its own; never /__reset (the contract suite shares the server).
import { execFileSync } from "node:child_process";

const PROJECT = process.env.COMPOSE_PROJECT ?? "helmstudio-test";
if (PROJECT === "helmstudio") throw new Error("e2e never scripts the dev stack's fake");
const C = `${PROJECT}-fake-openai-1`;

export type Sent = {
  model: string;
  body: { messages: { role: string; content: unknown }[]; tools?: unknown[] };
};

export function fake(method: string, path: string, body?: unknown): unknown {
  const out = execFileSync(
    "docker",
    [
      "exec",
      "-i",
      C,
      "python",
      "server.py",
      "call",
      method,
      path,
      ...(body === undefined ? [] : ["-"]),
    ],
    { input: body === undefined ? undefined : JSON.stringify(body) },
  );
  return JSON.parse(out.toString());
}

/** Queues replies for a model, in order. */
export const script = (model: string, ...replies: object[]) =>
  fake("POST", "/__script", { [model]: replies });

/** Every request the model got, oldest first (filtered in the container: the full log is large). */
export function sent(model: string): Sent[] {
  const py = `import json,sys,urllib.request
r=json.load(urllib.request.urlopen("http://127.0.0.1:8000/__requests",timeout=10))
print(json.dumps([{"model":x["model"],"body":x["body"]} for x in r if x.get("model")==sys.argv[1]]))`;
  const out = execFileSync("docker", ["exec", C, "python", "-c", py, model], {
    maxBuffer: 256 * 1024 * 1024,
  });
  return JSON.parse(out.toString());
}
