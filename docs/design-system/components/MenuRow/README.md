# MenuRow

A row in a picker popup (Run on, docs/mock/RunOn.dc.html, `mi`): a label over a mono sub-line, and a reason at the end.

- 8px 12px padding, `radius-field`, 13px `t1`; the sub-line is 11px mono `t2`; the reason (`em`) 11px `t2`.
- Hover `s3`; chosen (`aria-checked="true"`) `s4` and weight 600.
- Dim (`mi dim`): greyed but still pickable. Not selectable (`aria-disabled`): the label fades to 0.4, no hover, the reason stays readable.

The consumer provides the label, sub-line and reason. The rows shown are from the RunOn board.
