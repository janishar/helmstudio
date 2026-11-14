"""The graph of a batch of one workflow, one thread per row (thread_id "<batch_id>:<row>"):

    START → submit → wait → record → END        (submit → record when the reply is already terminal)

A step is two nodes. `submit` asks studio.runs.create (lane batch) under the idempotency key
<batch_id>:<row>:<row attempt>:step:0:0 and records the run id; asking again returns the same run. `wait` only
calls interrupt(): the thread parks until the engine resumes it with the run's result (run_finished), whatever the
outcome. `record` turns the result into the row's end. State is small plain JSON, never media."""

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TypedDict

from langgraph.graph import END, START, StateGraph
from langgraph.runtime import Runtime
from langgraph.types import interrupt

NODE, LOOP, BRANCH = "step", 0, "0"  # a batch of one workflow: one step, no loops, no branches
TERMINAL = ("succeeded", "failed", "cancelled")


class Row(TypedDict, total=False):
    batch_id: str
    row: int
    attempt: int
    inputs: dict
    run_id: str | None
    result: dict  # {status, take_ids, error}: the run's end, from the reply or run_finished
    status: str  # the row's end: succeeded | failed | cancelled
    take_ids: list[str]
    error: str | None


@dataclass
class Context:
    """Per batch, from the engine's batch record (never from state): who and what the runs are for."""

    create_run: Callable[[dict], Awaitable[dict]]
    owner_id: str
    session_id: str
    workflow_id: str


def key(batch_id: str, row: int, attempt: int) -> str:
    """The step's idempotency key."""
    return f"{batch_id}:{row}:{attempt}:{NODE}:{LOOP}:{BRANCH}"


def initial(batch_id: str, row: int, attempt: int, inputs: dict) -> Row:
    return {"batch_id": batch_id, "row": row, "attempt": attempt, "inputs": inputs}


async def submit(state: Row, runtime: Runtime[Context]) -> Row:
    """Ask studio for the row's run; a terminal reply (or a final error) is the result already."""
    ctx = runtime.context
    reply = await ctx.create_run(
        {
            "owner_id": ctx.owner_id,
            "session_id": ctx.session_id,
            "workflow_id": ctx.workflow_id,
            "inputs": state["inputs"],
            "lane": "batch",
            "batch_id": state["batch_id"],
            "row": state["row"],
            "idempotency_key": key(state["batch_id"], state["row"], state["attempt"]),
        }
    )
    if "code" in reply:  # invalid | not_found: final
        return {"run_id": None, "result": {"status": "failed", "error": reply["error"]}}
    out: Row = {"run_id": reply["run_id"]}
    if reply["status"] in TERMINAL:
        out["result"] = {k: reply.get(k) for k in ("status", "take_ids", "error")}
    return out


def wait(state: Row) -> Row:
    """Park until the run has finished. Nothing else here: a resumed node runs again from its first line."""
    return {"result": interrupt(state["run_id"])}


def record(state: Row) -> Row:
    """The row's end from its run's result."""
    r = state["result"]
    error = r.get("error") or ("the run failed" if r["status"] == "failed" else None)
    return {"status": r["status"], "take_ids": r.get("take_ids") or [], "error": error}


def after_submit(state: Row) -> str:
    return "record" if state.get("result") else "wait"


def build() -> StateGraph:
    g = StateGraph(Row, context_schema=Context)
    g.add_node("submit", submit)
    g.add_node("wait", wait)
    g.add_node("record", record)
    g.add_edge(START, "submit")
    g.add_conditional_edges("submit", after_submit, ["wait", "record"])
    g.add_edge("wait", "record")
    g.add_edge("record", END)
    return g
