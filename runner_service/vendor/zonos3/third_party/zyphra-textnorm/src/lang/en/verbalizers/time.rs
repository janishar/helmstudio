//! `en/verbalizers/time.py`.

use crate::lang::common::Fields;
use crate::tag::Dict;

fn tail(f: &mut Fields, out: &mut String, suffix: bool) -> Option<()> {
    if suffix {
        if let Some(s) = f.opt("suffix")? {
            out.push(' ');
            out.push_str(s);
        }
    }
    if let Some(z) = f.opt("zone")? {
        out.push(' ');
        out.push_str(z);
    }
    f.done().then_some(())
}

fn nonempty(s: &str) -> Option<&str> {
    (!s.is_empty()).then_some(s)
}

/// `hms` cdrewrite: delete `o ` after a space; `o one minutes|seconds` → `one minute|second` (−0.01, preferred).
fn fix_o(s: &str) -> String {
    let s = s.replace(" o one minutes", " one minute").replace(" o one seconds", " one second");
    s.replace(" o ", " ")
}

pub fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let h = nonempty(f.take("hours")?)?;
    let mut out = h.to_string();
    match f.peek_key() {
        Some("minutes") => {
            let m = nonempty(f.take("minutes")?)?;
            if f.peek_key() == Some("seconds") {
                let s = nonempty(f.take("seconds")?)?;
                out = format!("{h} hours {m} minutes and {s} seconds");
                if let Some(r) = out.strip_prefix("one hours") {
                    out = format!("one hour{r}");
                }
                out = fix_o(&out);
                tail(&mut f, &mut out, true)?;
                return Some(out);
            }
            out.push(' ');
            out.push_str(m);
            tail(&mut f, &mut out, true)?;
        }
        Some("suffix") => {
            let s = nonempty(f.take("suffix")?)?;
            out.push(' ');
            out.push_str(s);
            tail(&mut f, &mut out, false)?;
        }
        _ => {
            out.push_str(" o'clock");
            tail(&mut f, &mut out, false)?;
        }
    }
    Some(out)
}
