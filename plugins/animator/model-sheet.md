# Character design and the model sheet

## Design for animation

- Simple shapes: build the character from a few big shapes (a pear body, a round head, tube arms). Video models keep
  big shapes steady; small detail (patterns, many buttons, fine hair) flickers and changes between frames.
- A clear silhouette: the character should be readable filled black. Big ears, a tail, a hat, a scarf help.
- Limited colours: 3 to 5 main colours, named plainly ("orange fur, cream belly, dark brown paws, red scarf").
  Name the same colours, in the same order, in every prompt and in the element's text.
- Clear proportions: say them ("big head, about a third of the body", "short legs", "large round eyes").
- Different characters, different shapes: round vs. tall vs. square, so a wide shot still reads who is who.
- Write the design as one line and keep it: "@pip: a small fox, round head, big pointed ears, large round eyes,
  orange fur, cream belly, dark brown paws, red scarf". This line goes in the element's text.

## Sheet 1: turnaround

Qwen Image (recipes cartoon-sheet-draft, then cartoon-sheet), or MiniMax H3 Image (cartoon-sheet-h3) for more detail.

Prompt pattern:
"<style line>. Character model sheet of <design line>. Turnaround: the same character four times in a row, front
view, three-quarter view, side view, back view, full body, neutral standing pose, same size and height, evenly
spaced, plain white background, clean, no text."

- Make 2 to 4 seeds. Ask the user to pick one design.
- "same size and height" and "evenly spaced" keep the views comparable; "no text" stops fake labels.

## Sheet 2: expressions

Make it as an edit of the picked turnaround (put it in as a reference) so the design holds:
"Expression sheet of the character in <image1>, same design and style: six head close-ups in two rows, happy, sad,
angry, surprised, scared, sly grin; plain white background, no text."

- Expressions that read in cartoons are big and clear: wide eyes, a big open mouth, brows that move.
- Add a poses sheet the same way for an action-heavy film: "running, jumping, sitting, pointing, falling".

## Clean view for the element

- Make one clean image: "The character in <image1>, front view, full body, neutral pose, centred, plain white
  background, same design and style." Transparent (Qwen Image Options) gives a cut-out if the user wants one.
- saveAsElement with that take and a handle (`@pip`), group Character. Put the design line and the style line in
  its text.
- Give the element the character's voice once it is saved (editItem opens the element's Edit). Only a Character
  element keeps a voice; a Location or Prop has none, and a dialogue line for it fails.

## Places and props

- A place: "<style line>. Background painting of <place>, <time of day and light>, empty, no characters, wide
  shot." Save as `@orchard`, group Location.
- A prop that matters to the story: a small sheet like a character (front, side, in a hand for scale). Save as
  `@red-apple`, group Prop.
