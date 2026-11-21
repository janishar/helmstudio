//! C ABI (`include/zyphra_textnorm.h`, feature `capi`): UTF-8 in, UTF-8 out. Returned strings are `CString`s handed
//! to the caller, released by `zyphra_textnorm_free` (same allocator on both ends).
#![allow(unsafe_code)]

use std::borrow::Cow;
use std::ffi::{CString, c_char, c_int};

use crate::Normalizer;

/// The bytes the caller passed (`len` bytes at `ptr`; NULL or 0 is the empty string), lossily decoded.
unsafe fn input<'a>(ptr: *const c_char, len: usize) -> Cow<'a, str> {
    if ptr.is_null() || len == 0 {
        return Cow::Borrowed("");
    }
    // SAFETY: the caller guarantees `len` readable bytes at `ptr` for the duration of the call.
    let bytes = unsafe { std::slice::from_raw_parts(ptr.cast::<u8>(), len) };
    String::from_utf8_lossy(bytes)
}

fn output(s: String) -> *mut c_char {
    // An interior NUL would end the C string early; dropped.
    let s = match CString::new(s) {
        Ok(c) => c,
        Err(e) => {
            let mut bytes = e.into_vec();
            bytes.retain(|&b| b != 0);
            CString::new(bytes).expect("NUL bytes removed")
        }
    };
    s.into_raw()
}

/// See `zyphra_textnorm.h`.
///
/// # Safety
/// `tags` is NULL or a `\n`-separated list of `tags_len` readable bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zyphra_textnorm_new(tags: *const c_char, tags_len: usize) -> *mut Normalizer {
    // SAFETY: forwarded caller contract.
    let tags = unsafe { input(tags, tags_len) };
    Box::into_raw(Box::new(Normalizer::with_markup_tags(tags.split('\n').filter(|t| !t.is_empty()))))
}

/// See `zyphra_textnorm.h`.
///
/// # Safety
/// `tn` is NULL or a pointer returned by `zyphra_textnorm_new` and not yet freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zyphra_textnorm_delete(tn: *mut Normalizer) {
    if !tn.is_null() {
        // SAFETY: `tn` came from `Box::into_raw` in `zyphra_textnorm_new`.
        drop(unsafe { Box::from_raw(tn) });
    }
}

/// See `zyphra_textnorm.h`.
///
/// # Safety
/// `tn` is a live normalizer of `zyphra_textnorm_new`; `text` / `language` point to `text_len` / `language_len`
/// readable bytes (or are NULL with length 0).
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zyphra_textnorm_normalize(
    tn: *const Normalizer,
    text: *const c_char,
    text_len: usize,
    language: *const c_char,
    language_len: usize,
) -> *mut c_char {
    // SAFETY: forwarded caller contract.
    let (tn, text, language) = unsafe { (&*tn, input(text, text_len), input(language, language_len)) };
    output(tn.normalize(&text, &language))
}

/// See `zyphra_textnorm.h`.
///
/// # Safety
/// `text` points to `text_len` readable bytes (or is NULL with length 0).
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zyphra_textnorm_collapse_line_breaks(text: *const c_char, text_len: usize) -> *mut c_char {
    // SAFETY: forwarded caller contract.
    let text = unsafe { input(text, text_len) };
    output(crate::collapse_line_breaks(&text))
}

/// See `zyphra_textnorm.h`.
///
/// # Safety
/// `language` points to `language_len` readable bytes (or is NULL with length 0).
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zyphra_textnorm_supported(language: *const c_char, language_len: usize) -> c_int {
    // SAFETY: forwarded caller contract.
    let language = unsafe { input(language, language_len) };
    c_int::from(crate::supported(&language))
}

/// See `zyphra_textnorm.h`.
///
/// # Safety
/// `s` is NULL or a string returned by this library and not yet freed.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn zyphra_textnorm_free(s: *mut c_char) {
    if !s.is_null() {
        // SAFETY: `s` came from `CString::into_raw` in `output`.
        drop(unsafe { CString::from_raw(s) });
    }
}
