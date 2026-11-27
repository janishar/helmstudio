"use client";
// Expand (docs/mock/CreateAudio.dc.html, "The prompt, expanded"): a corner button on a multi-line text box, shown
// while its text overflows or on focus, that opens the same text in a big dialog with its count; the focus returns
// to the box on close. PromptArea uses it with `@` mentions; TextArea is a plain textarea with it.
import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
  type TextareaHTMLAttributes,
} from "react";
import { Maximize2 } from "lucide-react";
import { Modal } from "@/components/kit";

/** what the dialog's editor gets: its id, label, classes and the box's limits */
export type BigProps = {
  id: string;
  "aria-label": string;
  className: string;
  maxLength?: number;
  placeholder?: string;
};

/** The button and its dialog, inside the box's `group relative` wrapper. `editor` draws the dialog's textarea over
 * the same value (`mentions`: it takes `@` mentions, said in the hint). */
export function Expand({
  title,
  value,
  box,
  maxLength,
  placeholder,
  mentions,
  editor,
}: {
  title: string;
  value: string;
  /** the box it measures and gives the focus back to (a textarea, or the script's line list) */
  box: RefObject<HTMLElement | null>;
  maxLength?: number;
  placeholder?: string;
  mentions?: boolean;
  editor: (p: BigProps) => ReactNode;
}) {
  const id = "x" + useId().replace(/[^a-zA-Z0-9]/g, "");
  const [over, setOver] = useState(false);
  const [big, setBig] = useState(false);
  useLayoutEffect(() => {
    const t = box.current;
    // ponytail: measured on each change of the text only, not when the box is resized
    // eslint-disable-next-line react-hooks/set-state-in-effect -- a layout measure, read before paint
    if (t) setOver(t.scrollHeight > t.clientHeight);
  }, [box, value]);
  // the dialog's textarea takes the focus once the dialog is open (its own effect runs first)
  useEffect(() => {
    if (big) document.getElementById(id)?.focus();
  }, [big, id]);
  return (
    <>
      <button
        type="button"
        aria-label="Expand"
        title="Expand"
        aria-haspopup="dialog"
        // the box keeps the focus until the dialog takes it (a box that ends its editing on blur stays open)
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => setBig(true)}
        className={`absolute top-0 right-0 z-[1] grid size-7 place-items-center rounded-lg bg-s3 text-t2 hover:text-t1 ${over ? "" : "opacity-0 group-focus-within:opacity-100 focus-visible:opacity-100"}`}
      >
        <Maximize2 size={16} aria-hidden />
      </button>
      {big && (
        <Modal
          open
          onClose={() => {
            setBig(false);
            // after the browser puts the focus back on the button
            requestAnimationFrame(() => box.current?.focus());
          }}
          title={title}
          width="w-[min(94vw,880px)]"
        >
          <div className="flex flex-col gap-3.5">
            {editor({
              id,
              "aria-label": title,
              maxLength,
              placeholder,
              className:
                "block h-[min(420px,60vh)] w-full resize-none rounded-xl border border-a1 bg-s1 px-4 py-3.5 text-[15px] leading-6 text-t1 outline-0 placeholder:text-t2",
            })}
            <div className="flex items-center gap-2 text-[12px] text-t2">
              {maxLength != null && (
                <span className="mono">
                  {value.length} / {maxLength}
                </span>
              )}
              <span>
                {maxLength != null && "· "}
                {mentions ? "@ adds an element · Esc closes" : "Esc closes"}
              </span>
              <button
                type="button"
                onClick={(e) => e.currentTarget.closest("dialog")?.close()}
                className="ml-auto h-9 rounded-[10px] bg-a1 px-4 text-[13px] font-semibold text-[var(--on1)]"
              >
                Done
              </button>
            </div>
          </div>
        </Modal>
      )}
    </>
  );
}

/** A plain textarea with Expand titled `expand`; the dialog's textarea calls the same onChange. */
export function TextArea({
  expand,
  wrapClassName,
  ref,
  ...rest
}: TextareaHTMLAttributes<HTMLTextAreaElement> & {
  expand: string;
  value: string;
  /** the wrapper's classes (it takes the textarea's place in the layout) */
  wrapClassName?: string;
  ref?: RefObject<HTMLTextAreaElement | null>;
}) {
  const own = useRef<HTMLTextAreaElement>(null);
  const box = ref ?? own;
  return (
    <div className={`group relative flex flex-col ${wrapClassName ?? ""}`}>
      <textarea {...rest} ref={box} />
      {!rest.disabled && (
        <Expand
          title={expand}
          value={rest.value}
          box={box}
          maxLength={rest.maxLength}
          placeholder={rest.placeholder}
          editor={(p) => (
            <textarea
              {...p}
              spellCheck={rest.spellCheck}
              value={rest.value}
              onChange={rest.onChange}
              className={`${p.className} ${/\bmono\b/.test(rest.className ?? "") ? "mono" : ""}`}
            />
          )}
        />
      )}
    </div>
  );
}
