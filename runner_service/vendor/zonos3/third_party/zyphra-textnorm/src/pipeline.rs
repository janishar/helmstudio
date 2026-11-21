//! `Normalizer.normalize` (`normalize.py:338-401`), step for step, over a language's [`Grammar`].

use crate::lattice::{Classifier, NoPath, Shape, Tagger};
use crate::permute::{self, TooManyPermutations};
use crate::tag::{self, Dict, ParseError, Value};
use crate::text::{py_strip, pynini_escape};
use std::borrow::Cow;

/// One language's grammars: the tagger union, its top-level shape and the verbalizer.
pub trait Grammar: Send + Sync {
    /// NeMo language code (`en`, `zh`, …); selects the Moses detokenizer branches.
    fn code(&self) -> &'static str;
    fn shape(&self) -> Shape;
    /// The `classify` union in source order (each entry's top-level weight included in its candidates' cost).
    fn classifiers(&self) -> &[Box<dyn Classifier>];
    /// The separately wrapped punctuation item of the en-family shapes (`None` for zh/ja/ko).
    fn punct(&self) -> Option<&dyn Classifier>;
    /// Input rewrite composed before the sentence classifier, when its source grammar defines one.
    fn pre_classify<'t>(&self, text: &'t [char]) -> Cow<'t, [char]> {
        Cow::Borrowed(text)
    }
    /// `VerbalizeFinalFst` on one token: `tokens` holds the token body (`{"cardinal": {...}}`) in one field
    /// ordering. `None` when the verbalizer FST has no path for this ordering.
    fn verbalize(&self, token: &Dict) -> Option<String>;
    /// `select_verbalizer` on the first ordering of a whole split (`generate_permutations`) the verbalizer accepts, or
    /// `None` when it accepts none. The default verbalizes token by token (each token's first verbalizable ordering,
    /// joined with [`Grammar::token_join`]), which is the oracle's result whenever no verbalizer path crosses a token
    /// boundary. A grammar whose verbalizer can read across tokens (de's fraction numerator is any text) overrides
    /// this with [`permute::split_orderings`] and [`tag::serialize`].
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut pieces = Vec::with_capacity(split.len());
        for token in split {
            pieces.push(verbalize_token(self, token)?);
        }
        Some(pieces.join(self.token_join()))
    }
    /// How the verbalizer joins token outputs within a split: `" "` (en-family, ko) or `""` (zh, ja).
    fn token_join(&self) -> &'static str {
        " "
    }
    /// The language's `PostProcessingFst` on the stripped output; identity when none is defined.
    fn post_process(&self, text: String) -> String {
        text
    }
}

/// Exceptions that propagate out of `Normalizer.normalize` (the wrapper's sentence retry catches them).
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum GrammarError {
    /// `shortestpath(...).string()` on an empty tagger lattice.
    NoPath,
    /// `TokenParser` raised.
    Parse,
    /// An unsplittable token group with more than 729 permutations.
    TooManyPermutations,
    /// The wrapper's `ValueError("empty normalizer output")`.
    Empty,
    /// A grammar panicked (a port bug; the wrapper keeps the sentence as typed).
    Panic,
}

impl From<NoPath> for GrammarError {
    fn from(_: NoPath) -> Self {
        GrammarError::NoPath
    }
}
impl From<ParseError> for GrammarError {
    fn from(_: ParseError) -> Self {
        GrammarError::Parse
    }
}
impl From<TooManyPermutations> for GrammarError {
    fn from(_: TooManyPermutations) -> Self {
        GrammarError::TooManyPermutations
    }
}

/// The tagged string `select_tag` returns, as each `tokenize_and_classify.py` writes it.
pub fn tagged_string(g: &dyn Grammar, text: &[char]) -> Result<String, NoPath> {
    let text = g.pre_classify(text);
    let tagger = Tagger { shape: g.shape(), classifiers: g.classifiers(), punct: g.punct() };
    let items = tagger.best_path(&text)?;
    let len: usize = items.iter().filter_map(|i| i.candidate.as_ref()).map(|c| c.tag.len() + 12).sum();
    let mut out = String::with_capacity(len);
    for c in items.iter().filter_map(|i| i.candidate.as_ref()) {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str("tokens { ");
        out.push_str(&c.tag);
        out.push_str(" }");
    }
    crate::lattice::recycle_path(items);
    Ok(out)
}

/// The first verbalizable field ordering of one top-level token (`_permute` order), or `None`.
fn verbalize_token<G: Grammar + ?Sized>(g: &G, token: &Dict) -> Option<String> {
    // Each parsed item is {"tokens": {...}} (or another key: the oracle's verbalizer has no path for it).
    let [(key, Value::Dict(body))] = token.as_slice() else { return None };
    if key != "tokens" {
        return None;
    }
    permute::first_ordering(body, &mut |ordering: &Dict| g.verbalize(ordering))
}

thread_local! {
    // Like the lattice work buffers, reuse input storage without caching results.
    static INPUT_CHARS: std::cell::Cell<Vec<char>> = const { std::cell::Cell::new(Vec::new()) };
}

/// `DEBUG_TAGS=1` prints each grammar call's tagged string on stderr (read once).
static DEBUG_TAGS: std::sync::LazyLock<bool> = std::sync::LazyLock::new(|| std::env::var_os("DEBUG_TAGS").is_some());

/// `Normalizer.normalize(text, punct_post_process=...)`.
pub fn normalize(g: &dyn Grammar, text: &str, punct_post_process: bool) -> Result<String, GrammarError> {
    let original = text;
    let text = py_strip(text);
    if text.is_empty() {
        return Ok(String::new());
    }
    let mut chars = INPUT_CHARS.take();
    chars.extend(text.chars());
    let tagged = tagged_string(g, &chars);
    chars.clear();
    INPUT_CHARS.set(chars);
    let tagged = tagged?;
    if *DEBUG_TAGS {
        eprintln!("[{}] tags: {tagged}", g.code());
    }
    let tokens = tag::parse(&tagged)?;
    let splits = permute::split(&tokens)?;
    let multiple_splits = splits.len() > 1;
    let mut output = String::new();
    for (index, split) in splits.into_iter().enumerate() {
        // inside the oracle's try: any failure returns the (stripped, escaped) input
        if split.iter().any(|t| permute::has_none(t)) {
            return Ok(pynini_escape(text));
        }
        if split.is_empty() {
            // generate_permutations yields "" whose verbalizer lattice is empty: select_verbalizer raises
            return Ok(pynini_escape(text));
        }
        let Some(verbalized) = g.verbalize_split(split) else { return Ok(pynini_escape(text)) };
        if index == 0 {
            output = verbalized;
            if multiple_splits {
                output.reserve(tagged.len().saturating_sub(output.len()));
            }
        } else {
            output.push(' ');
            output.push_str(&verbalized);
        }
    }
    // Keep the owned output buffer: collapsing ASCII spaces needs no second copy.
    let mut previous_space = false;
    output.retain(|c| {
        let space = c == ' ';
        let keep = !space || !previous_space;
        previous_space = space;
        keep
    });
    if matches!(g.code(), "en" | "hi" | "vi") {
        let stripped = py_strip(&output);
        output = if stripped.is_empty() {
            String::new()
        } else if stripped.len() == output.len() {
            g.post_process(output)
        } else {
            g.post_process(stripped.to_string())
        };
    }
    if punct_post_process {
        output = crate::moses::detokenize(g.code(), &output);
        output = crate::punct_post::post_process_punct(original, &output);
    }
    Ok(output)
}
