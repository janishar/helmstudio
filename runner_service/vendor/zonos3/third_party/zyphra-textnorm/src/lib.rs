//! Text normalization for TTS: written text to its spoken form ("$5.32" → "five dollars thirty two cents").
//!
//! Native Rust ports of the NeMo-text-processing r1.2.0 grammars and ZONOS3's CLDR-based locale grammars,
//! for 43 languages, plus the `Normalizer` pipeline, Moses detokenizer, punctuation processing, and wrapper:
//! line-break collapsing, markup and verbatim spans kept out of the grammars, per-language fixes, a sentence
//! retry after a grammar failure, and a memo. No FST runtime: every grammar is Rust code, every grammar table is
//! embedded at compile time, nothing is loaded at runtime.
//!
//! ```
//! assert_eq!(zyphra_textnorm::normalize("It costs $5.32.", "en"), "It costs five dollars thirty two cents.");
//!
//! // Markup the speech model reads itself, kept as typed.
//! let tn = zyphra_textnorm::Normalizer::with_markup_tags(["laugh"]);
//! assert_eq!(tn.normalize("So I said <laugh/> no way, $5.", "en"), "So I said <laugh/> no way, five dollars.");
//! ```

// Only the C ABI (ffi.rs) is unsafe.
#![deny(unsafe_code)]

// The modules below are the port's internals, public for the differential tests (tests/): not part of the API.
#[cfg(feature = "capi")]
#[doc(hidden)]
pub mod ffi;
#[doc(hidden)]
pub mod lang;
#[doc(hidden)]
pub mod lattice;
#[doc(hidden)]
pub mod moses;
#[doc(hidden)]
pub mod permute;
#[doc(hidden)]
pub mod pipeline;
#[doc(hidden)]
pub mod punct_post;
#[doc(hidden)]
pub mod tag;
#[doc(hidden)]
pub mod text;
#[doc(hidden)]
pub mod wrapper;

use std::sync::LazyLock;

pub use wrapper::Normalizer;

/// The grammar languages (ISO 639-1; `zh` is Mandarin), in NeMo's names. [`supported`] says which this build has (one
/// cargo feature per language, all on by default).
pub const LANGUAGES: [&str; 43] = [
    "en", "de", "es", "fr", "it", "pt", "ja", "zh", "ko", "nl", "pl", "tr", "uk", "cs", "da", "fi", "el", "nb", "ro",
    "sk", "bg", "he", "th", "tl", "ms", "ka", "ta", "gu", "kn", "ml", "pa", "or", "ru", "sv", "hu", "ar", "vi", "id",
    "hi", "bn", "te", "mr", "ur",
];

/// Whether this build has a grammar for `language` (one of [`LANGUAGES`]).
pub fn supported(language: &str) -> bool {
    lang::grammar(language).is_some()
}

/// One plain space for every whitespace run that contains a line break, tab or other non-space whitespace; runs of
/// plain spaces are kept.
pub fn collapse_line_breaks(text: &str) -> String {
    wrapper::collapse_line_breaks(text).into_owned()
}

static DEFAULT: LazyLock<Normalizer> = LazyLock::new(Normalizer::new);

/// The spoken form of `text` in `language`, by a shared [`Normalizer::new`] (no markup tags; memoized). See
/// [`Normalizer::normalize`].
pub fn normalize(text: &str, language: &str) -> String {
    DEFAULT.normalize(text, language)
}
