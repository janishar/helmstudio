// Copyright (c) 2026 Zyphra, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost, is_nemo_space};

pub const WEIGHT: Cost = cost(200.0);

pub struct Word;

impl Lazy for Word {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() || is_nemo_space(text[start]) {
            return;
        }
        let mut end = start + 1;
        while end < text.len() && !is_nemo_space(text[end]) {
            end += 1;
        }
        out.push(end, WEIGHT, 0, || {
            let word: String = text[start..end].iter().collect();
            format!("name: \"{word}\"")
        });
    }
}
