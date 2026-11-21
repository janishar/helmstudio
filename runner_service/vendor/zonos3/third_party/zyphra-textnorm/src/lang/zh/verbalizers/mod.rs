//! `zh/verbalizers/*.py`: `VerbalizeFinalFst` on one token body.
//!
//! The verbalizers read the serialized token (`_permute`'s text for one field ordering, rebuilt with
//! [`tag::serialize`]) the way the FSTs do, character by character, so values holding a stray `"` behave as upstream
//! (zh money reads `currency: "块"integer_part: "三" currency_maj: "毛"` as two amounts). `verbalize_final.py` composes
//! with `PostProcessor(remove_puncts=False, to_upper=False, to_lower=False, tag_oov=False)`, the identity, so
//! neither the post-processor nor `post_processing.py` (not used by `Normalizer`) is ported.

mod cardinal;
mod date;
mod decimal;
mod fraction;
mod measure;
mod money;
mod ordinal;
mod time;
mod whitelist;

use std::cell::RefCell;

use crate::tag::{self, Dict, Value};
use crate::text::is_nemo_space;

/// A read position in the serialized token.
#[derive(Clone, Copy)]
pub struct Cur<'a> {
    s: &'a [char],
    i: usize,
}

impl<'a> Cur<'a> {
    /// `delete_space`: every whitespace char (the FST's split is chosen by what follows; see [`Cur::sp_lit`]).
    pub fn ws(&mut self) {
        while self.i < self.s.len() && is_nemo_space(self.s[self.i]) {
            self.i += 1;
        }
    }

    pub fn lit(&mut self, l: &str) -> bool {
        let mut j = self.i;
        for c in l.chars() {
            if self.s.get(j) != Some(&c) {
                return false;
            }
            j += 1;
        }
        self.i = j;
        true
    }

    /// `delete_space + delete(rest)` where `rest` starts with a space: the whitespace run ends with the space `rest`
    /// needs.
    pub fn sp_lit(&mut self, rest: &str) -> bool {
        let start = self.i;
        self.ws();
        if self.i > start && self.s[self.i - 1] == ' ' {
            self.i -= 1;
            if self.lit(rest) {
                return true;
            }
        }
        self.i = start;
        false
    }

    /// `closure(NEMO_NOT_QUOTE)` up to (not including) the next `"`; `None` when no `"` follows.
    pub fn not_quote(&mut self) -> Option<&'a [char]> {
        let start = self.i;
        while self.i < self.s.len() && self.s[self.i] != '"' {
            self.i += 1;
        }
        (self.i < self.s.len()).then(|| &self.s[start..self.i])
    }

    /// `delete(open) + closure(NEMO_NOT_QUOTE) + delete("\"")` where `open` ends with the opening quote.
    pub fn field(&mut self, open: &str) -> Option<&'a [char]> {
        let save = self.i;
        if self.lit(open)
            && let Some(v) = self.not_quote()
        {
            self.i += 1;
            return Some(v);
        }
        self.i = save;
        None
    }

    /// `delete(key + ":") + delete_space + delete("\"") + closure(NEMO_NOT_QUOTE) + delete("\"")`.
    pub fn field_ws(&mut self, key_colon: &str) -> Option<&'a [char]> {
        let save = self.i;
        if self.lit(key_colon) {
            self.ws();
            if let Some(v) = self.field("\"") {
                return Some(v);
            }
        }
        self.i = save;
        None
    }

    pub fn peek(&self, l: &str) -> bool {
        let mut c = *self;
        c.lit(l)
    }

    pub fn at_end(&self) -> bool {
        self.i == self.s.len()
    }
}

/// `GraphFst.delete_tokens`: `name delete_space { delete_space fst delete_space }`, then NBSP → space.
fn delete_tokens(c: &mut Cur, name: &str, fst: impl FnOnce(&mut Cur, &mut String) -> bool, out: &mut String) -> bool {
    if !c.lit(name) {
        return false;
    }
    c.ws();
    if !c.lit("{") {
        return false;
    }
    c.ws();
    let mark = out.len();
    if !fst(c, out) {
        return false;
    }
    c.ws();
    if !c.lit("}") {
        return false;
    }
    // NBSP is C2 A0 in UTF-8, and 0xC2 only ever starts a two-byte char: a byte scan rules most outputs out
    if out.as_bytes()[mark..].contains(&0xC2) && out[mark..].contains('\u{a0}') {
        let fixed = out[mark..].replace('\u{a0}', " ");
        out.truncate(mark);
        out.push_str(&fixed);
    }
    true
}

/// `delete_tokens(class, fst)` on a parsed `class { … }` token whose field values are strings without `"`: the
/// serialized text then reads back field for field, so date, time and money's unit shape are verbalized from the
/// fields directly. `None` when the token is not of that kind (the text reader decides).
fn direct(token: &Dict, out: &mut String) -> Option<bool> {
    let [(class, Value::Dict(body))] = token.as_slice() else { return None };
    let mut fields: [(&str, &str); 8] = [("", ""); 8];
    if body.len() > fields.len() {
        return None;
    }
    for (slot, (k, v)) in fields.iter_mut().zip(body) {
        let Value::Str(v) = v else { return None };
        if v.contains('"') {
            return None;
        }
        *slot = (k.as_ref(), v.as_ref());
    }
    let fields = &fields[..body.len()];
    let mark = out.len();
    let ok = match class.as_ref() {
        "date" => date::direct(fields, out),
        "time" => time::direct(fields, out),
        "money" => money::direct(fields, out),
        _ => None,
    }?;
    if !ok {
        out.truncate(mark);
    } else if out.as_bytes()[mark..].contains(&0xC2) && out[mark..].contains('\u{a0}') {
        let fixed = out[mark..].replace('\u{a0}', " ");
        out.truncate(mark);
        out.push_str(&fixed);
    }
    Some(ok)
}

thread_local! {
    /// The serialized token and its chars (reused across calls).
    static SCRATCH: RefCell<(String, Vec<char>)> = const { RefCell::new((String::new(), Vec::new())) };
}

/// `VerbalizeFst` (`closure(delete_space) + union + closure(delete_space)`) on one token body, appended to `out`;
/// `false` (nothing appended) when no branch reads the whole token.
pub fn verbalize_into(token: &Dict, out: &mut String) -> bool {
    // `name: "v"` with a quote-free `v`: word (one char) or whitelist (any), both output `v` as is
    if let [(k, Value::Str(v))] = token.as_slice()
        && k == "name"
        && !v.contains('"')
    {
        out.push_str(v);
        return true;
    }
    if let Some(ok) = direct(token, out) {
        return ok;
    }
    SCRATCH.with_borrow_mut(|(text, chars)| {
        text.clear();
        tag::serialize(token, text);
        chars.clear();
        chars.extend(text.chars());
        let start = Cur { s: chars, i: 0 };
        type Branch = fn(&mut Cur, &mut String) -> bool;
        // Each `delete_tokens` branch opens with its own class name, which no other branch's text starts with, so
        // the union is decided by the token's first key (union order kept for `name`: word, then whitelist).
        let branches: &[Branch] = match token.first().map(|(k, _)| k.as_ref()) {
            Some("date") => &[|c, o| delete_tokens(c, "date", date::date, o)],
            Some("cardinal") => &[|c, o| delete_tokens(c, "cardinal", cardinal::cardinal, o)],
            Some("ordinal") => &[|c, o| delete_tokens(c, "ordinal", ordinal::ordinal, o)],
            Some("decimal") => &[|c, o| delete_tokens(c, "decimal", decimal::decimal, o)],
            Some("fraction") => &[|c, o| delete_tokens(c, "fraction", fraction::fraction, o)],
            Some("money") => &[|c, o| delete_tokens(c, "money", money::money, o)],
            Some("measure") => &[|c, o| delete_tokens(c, "measure", measure::measure, o)],
            Some("time") => &[|c, o| delete_tokens(c, "time", time::time, o)],
            _ => &[whitelist::word, whitelist::whitelist],
        };
        let mark = out.len();
        for branch in branches {
            let mut c = start;
            c.ws();
            if branch(&mut c, out) {
                c.ws();
                if c.at_end() {
                    return true;
                }
            }
            out.truncate(mark);
        }
        false
    })
}

pub fn push(out: &mut String, v: &[char]) {
    out.extend(v);
}
