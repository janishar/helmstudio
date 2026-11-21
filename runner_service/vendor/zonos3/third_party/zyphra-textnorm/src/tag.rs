//! The tagged string between tagger and verbalizer: a literal port of `token_parser.py`.
//!
//! Classifiers write the tagger's output text verbatim (`cardinal { integer: "one" }`); the pipeline wraps it in
//! `tokens { … }` exactly as each language's `tokenize_and_classify.py` does and then parses it back with this
//! parser, so every quirk of the upstream grammar's text (and every parser failure) is reproduced.

use std::borrow::Cow;

/// A parsed field value. `None` is an empty quoted value (`_permute` rejects it later). Parsed text borrows from the
/// tagged string; verbalizers that re-split a field build owned values.
#[derive(Clone, Debug, PartialEq)]
pub enum Value<'a> {
    Str(Cow<'a, str>),
    None,
    Dict(Dict<'a>),
    True,
}

/// One `key: value` field.
pub type Field<'a> = (Cow<'a, str>, Value<'a>);

/// An ordered dict with unique keys (Python `OrderedDict`).
pub type Dict<'a> = Vec<Field<'a>>;

/// The upstream parser raised (`AssertionError`, `ValueError`, `IndexError`); propagates out of `normalize`.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct ParseError;

/// Works on the UTF-8 bytes: every character the parser tests (`"`, `{`, `}`, `:`, space, key letters, ASCII
/// whitespace) is ASCII, and a non-ASCII character is never one of them, so byte positions stand in for the Python
/// character positions and value slices fall on character boundaries.
struct Parser<'a> {
    text: &'a str,
    bytes: &'a [u8],
    index: usize,
    /// `None` is the `EOS` sentinel; non-ASCII bytes are compared as themselves (never an ASCII character).
    char: Option<u8>,
}

/// `[A-Za-z_]`: the parser's key characters.
fn is_key_char(c: u8) -> bool {
    c.is_ascii_alphabetic() || c == b'_'
}

/// Python `str.isspace()` on the ASCII range the parser can meet before a key.
fn is_py_ascii_space(c: u8) -> bool {
    matches!(c, b' ' | b'\t' | b'\n' | b'\r' | 0x0b | 0x0c)
}

impl<'a> Parser<'a> {
    /// Advance one character (UTF-8 continuation bytes skipped).
    fn read(&mut self) -> bool {
        let mut next = self.index + 1;
        while next < self.bytes.len() && (self.bytes[next] & 0xC0) == 0x80 {
            next += 1;
        }
        if next < self.bytes.len() {
            self.index = next;
            self.char = Some(self.bytes[next]);
            true
        } else {
            self.char = None;
            false
        }
    }

    fn parse_ws(&mut self) -> bool {
        let mut not_eos = self.char.is_some();
        while not_eos && self.char == Some(b' ') {
            not_eos = self.read();
        }
        not_eos
    }

    fn parse_char(&mut self, exp: u8) -> Result<(), ParseError> {
        if self.char != Some(exp) {
            return Err(ParseError);
        }
        self.read();
        Ok(())
    }

    fn parse_string_key(&mut self) -> Result<Option<&'a str>, ParseError> {
        match self.char {
            None => return Err(ParseError),
            Some(c) if is_py_ascii_space(c) => return Err(ParseError),
            _ => {}
        }
        let start = self.index;
        while self.char.is_some_and(is_key_char) {
            if !self.read() {
                return Err(ParseError);
            }
        }
        Ok((self.index > start).then(|| &self.text[start..self.index]))
    }

    fn parse_string_value(&mut self) -> Result<Value<'a>, ParseError> {
        if self.char.is_none() {
            return Err(ParseError);
        }
        let start = self.index;
        // The value runs to the first `"` followed by a space (a byte search: `"` and ` ` are ASCII, never inside a
        // multi-byte character). A quote as the last character is upstream's `IndexError`; no such quote at all
        // reads into the EOS sentinel, which fails the same way.
        let mut from = start;
        let end = loop {
            let Some(q) = self.bytes[from..].iter().position(|&b| b == b'"').map(|q| from + q) else {
                return Err(ParseError);
            };
            match self.bytes.get(q + 1) {
                None => return Err(ParseError),
                Some(b' ') => break q,
                Some(_) => from = q + 1,
            }
        };
        self.index = end;
        self.char = Some(b'"');
        let value = &self.text[start..end];
        Ok(if value.is_empty() { Value::None } else { Value::Str(Cow::Borrowed(value)) })
    }

    fn parse_token_value(&mut self) -> Result<Value<'a>, ParseError> {
        match self.char {
            Some(b':') => {
                self.parse_char(b':')?;
                self.parse_ws();
                self.parse_char(b'"')?;
                let v = self.parse_string_value()?;
                self.parse_char(b'"')?;
                Ok(v)
            }
            Some(b'{') => {
                self.parse_char(b'{')?;
                // `parse()` then a dict merge of its single-key items: a repeated key overwrites in place
                let mut d: Dict<'a> = Vec::new();
                while self.parse_ws() {
                    let Some((k, v)) = self.parse_token()? else { break };
                    match d.iter_mut().find(|(key, _)| *key == k) {
                        Some(slot) => slot.1 = v,
                        None => d.push((k, v)),
                    }
                }
                self.parse_char(b'}')?;
                Ok(Value::Dict(d))
            }
            _ => Err(ParseError),
        }
    }

    /// One `key: value` item (a single-key dict upstream).
    fn parse_token(&mut self) -> Result<Option<Field<'a>>, ParseError> {
        let Some(key) = self.parse_string_key()? else {
            return Ok(None);
        };
        self.parse_ws();
        let value = if key == "preserve_order" {
            self.parse_char(b':')?;
            self.parse_ws();
            for c in *b"true" {
                self.parse_char(c)?;
            }
            Value::True
        } else {
            self.parse_token_value()?
        };
        Ok(Some((Cow::Borrowed(key), value)))
    }

    fn parse(&mut self) -> Result<Vec<Dict<'a>>, ParseError> {
        let mut l = Vec::new();
        while self.parse_ws() {
            match self.parse_token()? {
                Some(item) => l.push(vec![item]),
                None => break,
            }
        }
        Ok(l)
    }
}

/// `TokenParser()(text); parser.parse()`: a list of single-key dicts (`{"tokens": {...}}`).
pub fn parse(text: &str) -> Result<Vec<Dict<'_>>, ParseError> {
    let bytes = text.as_bytes();
    let Some(&first) = bytes.first() else { return Err(ParseError) };
    let mut p = Parser { text, bytes, index: 0, char: Some(first) };
    p.parse()
}

/// `_permute`'s serialization of one field ordering: `k: "v" `, ` k { … } `, `k: true `. A parsed item
/// `[("tokens", Dict(body))]` serializes to the ` tokens { … } ` string `generate_permutations` concatenates. The caller
/// has checked `permute::has_none` (`_permute` raises on an empty value).
pub fn serialize(d: &Dict, out: &mut String) {
    for (k, v) in d {
        match v {
            Value::Str(s) => {
                out.push_str(k);
                out.push_str(": \"");
                out.push_str(s);
                out.push_str("\" ");
            }
            Value::Dict(inner) => {
                out.push(' ');
                out.push_str(k);
                out.push_str(" { ");
                serialize(inner, out);
                out.push_str(" } ");
            }
            Value::True => {
                out.push_str(k);
                out.push_str(": true ");
            }
            Value::None => unreachable!("_permute raises on an empty value; checked by permute::has_none"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn p(s: &str) -> Result<Vec<Dict<'_>>, ParseError> {
        parse(s)
    }

    #[test]
    fn parses_nested_tokens() {
        let r = p("tokens { money { integer: \"20\" currency: \"$\" } } tokens { name: \"left\" }").unwrap();
        assert_eq!(r.len(), 2);
        let (k, v) = &r[0][0];
        assert_eq!(k, "tokens");
        let Value::Dict(d) = v else { panic!() };
        let Value::Dict(m) = &d[0].1 else { panic!() };
        assert_eq!(m[0], ("integer".into(), Value::Str("20".into())));
        assert_eq!(m[1], ("currency".into(), Value::Str("$".into())));
        assert_eq!(r[1][0].1, Value::Dict(vec![("name".into(), Value::Str("left".into()))]));
    }

    #[test]
    fn preserve_order_and_quotes() {
        let r = p("tokens { date { day: \"1\" preserve_order: true } }").unwrap();
        let Value::Dict(d) = &r[0][0].1 else { panic!() };
        let Value::Dict(m) = &d[0].1 else { panic!() };
        assert_eq!(m[1], ("preserve_order".into(), Value::True));
        // the upstream fr date tagger emits `preserve_order: true}` — parses as well
        assert!(p("tokens { date { day: \"1\" preserve_order: true} }").is_ok());
        // a quote run is a value: `"` is ended only by a quote followed by a space
        let r = p("tokens { name: \"\"\"\" }").unwrap();
        let Value::Dict(d) = &r[0][0].1 else { panic!() };
        assert_eq!(d[0].1, Value::Str("\"\"".into()));
        let r = p("tokens { name: \"\" }").unwrap();
        let Value::Dict(d) = &r[0][0].1 else { panic!() };
        assert_eq!(d[0].1, Value::None);
    }

    #[test]
    fn duplicate_keys_overwrite_in_place() {
        let r = p("tokens { a: \"1\" b: \"2\" a: \"3\" }").unwrap();
        let Value::Dict(d) = &r[0][0].1 else { panic!() };
        assert_eq!(d, &vec![("a".into(), Value::Str("3".into())), ("b".into(), Value::Str("2".into()))]);
    }

    #[test]
    fn non_ascii_values_and_quotes() {
        // multi-byte characters inside values, next to the closing quote, and as the char after a quote
        let r = p("tokens { name: \"五点五\" } tokens { name: \"\"€\" }").unwrap();
        let Value::Dict(d) = &r[0][0].1 else { panic!() };
        assert_eq!(d[0].1, Value::Str("五点五".into()));
        let Value::Dict(d) = &r[1][0].1 else { panic!() };
        assert_eq!(d[0].1, Value::Str("\"€".into()));
        assert_eq!(p("tokens { name: \"é\""), Err(ParseError));
    }

    #[test]
    fn errors() {
        assert_eq!(p("tokens { name: \"a\""), Err(ParseError)); // quote as last char → IndexError
        assert_eq!(p("tokens { name \"a\" }"), Err(ParseError)); // neither ':' nor '{'
        assert_eq!(p("tokens"), Err(ParseError)); // key runs into EOS
    }
}
