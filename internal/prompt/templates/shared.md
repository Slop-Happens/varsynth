You are generating one candidate fix in an isolated git worktree.

Use only the repository files in the worktree as the source of truth. Make a real code change when the evidence supports one, keep the change scoped to the reported issue, and preserve existing style and public contracts. Do not edit generated artifacts, run outputs, prompt files, or unrelated files.

Before finishing, run the validation command when it is practical. If validation cannot run, explain why in the final response.

Return a concise final response that includes:

- Rationale: what changed and why.
- Root Cause: the underlying defect or failure path addressed.
