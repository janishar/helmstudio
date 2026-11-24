"use client";
// Skills in the drawer (docs/mock/Assistant.dc.html frame 16; API 1.64): the "Using skill" line (tap to see the
// guide's first lines and the files it read) and the @ picker's Skills group.
import { useEffect, useState } from "react";
import { Book, Check, ChevronDown } from "lucide-react";
import { api, message, ok, type S } from "@/lib/api";
import { guideLines, skillTitle } from "@/lib/skills";

type Line = S<"AssistantLine">;
type Summary = S<"SkillSummary">;

/** One Using skill line: closed, the title and a chevron; open, built in|yours · version and the box. */
export function SkillLine({ line, reads }: { line: Line; reads: string[] }) {
  const [open, setOpen] = useState(false);
  const [skill, setSkill] = useState<S<"Skill"> | null>(null);
  const [error, setError] = useState("");
  const title = skillTitle(line);
  function toggle() {
    setOpen(!open);
    if (skill || !line.skill) return;
    setError("");
    ok(api.GET("/v1/studio/skills/{skill}", { params: { path: { skill: line.skill } } }))
      .then(setSkill)
      .catch((e) => setError(message(e)));
  }
  return (
    <>
      <button
        type="button"
        className="skl"
        aria-expanded={open}
        aria-label={`Using skill: ${title} · tap to ${open ? "hide" : "see"}`}
        onClick={toggle}
      >
        <Book size={14} strokeWidth={2} aria-hidden />
        <span>
          Using skill: <b>{title}</b>
        </span>
        {open ? (
          skill && (
            <small>
              {skill.origin === "yours" ? "yours" : "built in"} · {skill.version}
            </small>
          )
        ) : (
          <ChevronDown size={12} strokeWidth={2} aria-hidden />
        )}
      </button>
      {open && (
        <div className="skb">
          {error ? (
            <p className="text-bad">{error}</p>
          ) : !skill ? (
            <p className="skf">…</p>
          ) : (
            <Guide title={skill.title} text={skill.text} />
          )}
          {reads.map((f) => (
            <p key={f} className="skf">
              <Check size={14} strokeWidth={2.5} aria-hidden className="ok" />
              Read {f}
            </p>
          ))}
        </div>
      )}
    </>
  );
}

/** The guide's first lines: paragraphs, and bullets as a list; the title in bold before the first one. */
function Guide({ title, text }: { title: string; text: string }) {
  const out: React.ReactNode[] = [];
  let items: string[] = [];
  const flush = () => {
    if (items.length)
      out.push(
        <ul key={`u${out.length}`}>
          {items.map((t, i) => (
            <li key={i}>{t}</li>
          ))}
        </ul>,
      );
    items = [];
  };
  guideLines(text).forEach((b, i) => {
    if (b.li) return void items.push(b.text);
    flush();
    out.push(
      <p key={`p${i}`}>
        {i === 0 && <b>{title}</b>}
        {i === 0 && " · "}
        {b.text}
      </p>,
    );
  });
  flush();
  return <>{out}</>;
}

/** The skills that are on (GET /v1/studio/skills), read again each time the picker opens. */
export function useSkills(want: boolean) {
  const [list, setList] = useState<Summary[]>([]);
  useEffect(() => {
    if (want)
      ok(api.GET("/v1/studio/skills"))
        .then((r) => setList(r.skills))
        .catch(() => setList([]));
  }, [want]);
  return list;
}

/** The @ picker above the composer: what was typed, then Skills (the only group in the drawer for now). */
export function SkillPicker({
  query,
  items,
  hi,
  onPick,
}: {
  query: string;
  items: Summary[];
  hi: number;
  onPick: (s: Summary) => void;
}) {
  return (
    <div className="pop atp" role="listbox" aria-label="Pick a skill">
      <div className="atq">{query}</div>
      <h4>Skills</h4>
      {items.map((s, i) => (
        <button
          key={s.name}
          type="button"
          role="option"
          aria-selected={i === hi}
          className={`pr ${i === hi ? "on" : ""}`}
          title={s.when}
          onMouseDown={(e) => e.preventDefault()} // the composer keeps the focus
          onClick={() => onPick(s)}
        >
          <i>
            <Book size={14} strokeWidth={2} aria-hidden />
          </i>
          <span>@{s.name}</span>
          <small>skill</small>
        </button>
      ))}
    </div>
  );
}
