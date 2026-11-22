/* helmstudio's patch, not upstream h3.c (VERSION, patches/h3-lora.diff):
 * merge a Diffusers/PEFT MiniMax-H3 LoRA (lightx2v's Turbo) into the DiT's
 * BF16 weights as h3 loads them, before its int8 quantizer sees them. */
#ifndef H3_LORA_H
#define H3_LORA_H

#include "h3_gpu.h"

#include <stdint.h>

/* With env H3_LORA set to a .safetensors file (H3_LORA_STRENGTH, default 1),
 * add its delta for the native weight `name` into `tensor` (BF16, rows x
 * cols) in place; a weight the LoRA does not touch is left as it is.
 * Returns 0 on a bad file or a read/write failure (an "h3 lora: " line says
 * why; the caller fails with "cannot merge LoRA into <name>"). */
int h3_lora_apply(const char *name, h3_gpu_tensor *tensor, uint64_t rows,
                  uint64_t cols);

#endif
