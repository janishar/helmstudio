"""Pack `text`: plain-Python custom nodes on text (orchestration only: no model, no network)."""

from pydantic import BaseModel, Field

from custom import Custom


class PrefixIn(BaseModel):
    text: str = Field(max_length=4000)
    prefix: str = Field(max_length=200)


class PrefixOut(BaseModel):
    text: str = Field(max_length=4201)


async def prefix(inp: PrefixIn, ctx) -> PrefixOut:
    return PrefixOut(text=inp.prefix + inp.text)


NODES = [
    Custom(
        "text/prefix",
        "Prefix text",
        "Puts a fixed prefix before a text (an example custom node: plain Python, no model, no network).",
        PrefixIn,
        PrefixOut,
        prefix,
    )
]
