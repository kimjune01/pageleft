# Public Domain Indexing Criteria

Public domain content is aligned with PageLeft's ethos: freely buildable-upon, attribution-preserving, durable. The same properties that make copyleft work — anyone can read, build from, and extend — apply to public domain by default.

The filter is the same one PageLeft applies everywhere: **does this page compile?** A public domain page is worth indexing if an agent reading it could produce a working artifact — code, a design, a proof, a protocol. Or if loading it into context measurably improves the agent's output on domain-specific tasks. Two compilation modes: artifact and judgment.

## What to index

### RFCs and protocol specs

Already spec-depth. RFC 2616 (HTTP/1.1) literally compiles into a web server. Public domain by IETF policy. The entire RFC corpus is buildable.

### US government technical reports

NASA technical memoranda, NIST standards, military field manuals. Written for implementors, not readers. Public domain by law. FM 5-34 (Engineer Field Data) is a vibeloggable construction manual.

### Expired patents

A patent's claims section is a spec. The detailed description is the build instruction. Pre-1928 or expired utility patents from USPTO full text. Rich source of "how things work" at implementation depth.

### Pre-1928 scientific and engineering papers

Shannon, Turing, but also the obscure ones: Shewhart's original quality control papers, early operations research, Heaviside's operator methods. Under-represented in LLM training, still load-bearing. The value is in what's *not* already in the weights.

### Open standards

W3C specs, POSIX, Unicode standard. Not always public domain legally, but often freely licensed and spec-depth. Verify license before indexing.

### Textbooks with worked examples

See [interactive textbooks](#priority-vertical-interactive-textbooks) below — this is PageLeft's priority vertical.

## Priority vertical: interactive textbooks

Public domain textbooks are the highest-leverage content for PageLeft. The structure is already there: definitions, theorems, proofs, exercises. An agent reads the textbook, produces an interactive webpage — draggable diagrams, step-through proofs, explorable explanations, parametric calculators. The textbook is the spec, the webpage is the artifact.

The pattern: any textbook where the exercises have *computable* answers. Not "discuss the themes of" but "prove that" or "calculate the" or "construct the."

### Validated sources

These compile into interactive pages. Natural Breadcrumbs has built or is building from them.

- **Milewski's Category Theory for Programmers** (CC BY-SA 4.0) — 24 chapters, each a breadcrumbs page. Haskell → Python translation is mechanical. Every concept has a computable example.
- **Grinstead & Snell's Introduction to Probability** (GFDL) — conditional probability through CLT. Modern notation, worked examples, computable exercises.
- **Judson's Abstract Algebra: Theory and Applications** (GFDL) — groups through universal algebra. Has Sage integration, maps directly to Python REPLs.

### Candidate sources

Plausible but unproven. The content is computable in principle; the translation cost varies.

- **Geometry** — Euclid's Elements, Hilbert's Foundations of Geometry. Inherently visual, every proof is a diagram waiting to be interactive.
- **Calculus** — Silvanus Thompson's Calculus Made Easy (1914). Step-through derivations. Modern notation, written for learners — likely low friction.
- **Physics** — Maxwell's Treatise, Rayleigh's Theory of Sound. Simulations from first principles. Higher friction — dense notation, continuous math that needs discretization.
- **Logic** — Whitehead & Russell's Principia (1910-1913). Early propositional sections (*1–*5) compile cleanly — every theorem is a boolean function verifiable by truth table. Later sections (ramified type theory, reducibility axiom) are genuinely hard to make interactive. Archaic notation (`⊃` for `→`, dot-grouping) adds a translation layer that modern sources don't need.
- **Statistics** — Fisher's early papers (1920s). Interactive distributions, sampling demos.
- **Engineering** — Machinery's Handbook early editions, shop manuals. Parametric calculators from the tables.

### Reference implementation: Natural Breadcrumbs

[Natural Breadcrumbs](https://github.com/kimjune01/natural-breadcrumbs) is this pattern already running. It takes copyleft/GFDL textbooks and turns each chapter into an interactive page with runnable Python REPLs, SVG diagrams, and notation tables. Sources already in production or planned:

- **Milewski's Category Theory for Programmers** (CC BY-SA 4.0) — 24 chapters, each a breadcrumbs page
- **Grinstead & Snell's Introduction to Probability** (GFDL) — conditional probability through CLT, ~10 pages
- **Judson's Abstract Algebra: Theory and Applications** (GFDL) — groups through universal algebra, ~8 pages

The textbook is the spec. The interactive page is the artifact. The agent reads a chapter, produces a page with step-through proofs, explorable examples, and dual REPLs (Python for programmers, Scheme for academics). See `FOUNDATIONS-PLAN.md` in that repo for the full expansion plan.

Every derivative page is CC BY-SA 4.0 and indexed on PageLeft automatically.

### The artifact test

A textbook page is worth indexing if an agent can turn it into one of: an interactive visualization, a step-through proof, an explorable simulation, or a parametric calculator. If the content is expository with no computable core, it fails the test.

## Second vertical: theory texts

Not all compilation produces artifacts you can see. Some texts compile into better judgment.

[Theory Is Load-Bearing](https://june.kim/theory-is-load-bearing) ([experiment repo](https://github.com/kimjune01/metacognition), [preregistration](https://june.kim/round3-preregistration)) tested this directly: loading the Natural Framework into an LLM's context improved diagnostic quality on data-processing tasks (P=0.949). A compressed checklist — same content, 16× fewer tokens, theory stripped out — was worthless or harmful. The theoretical grounding is what makes the vocabulary applicable. You can't extract just the "what" and ship it without the "why."

This is a different compilation mode. The artifact isn't a visualization or a calculator. It's improved reasoning on domain-specific problems. Theory in, better diagnostics out.

### The test

A philosophy or theory text is worth indexing if loading it into an agent's context measurably improves output on tasks in its domain. Not "is this interesting to read" but "does this make the agent better at something specific?"

### Candidates

Philosophy that grounds a computable practice. The "why" behind a domain's "what."

- **Aristotle's Organon** ([Prior Analytics, Project Gutenberg](https://www.gutenberg.org/ebooks/author/564)) — syllogistic inference rules. Grounded formal logic. The rules are mechanizable; the theory of what makes a valid argument is load-bearing for any reasoning task.
- **Peirce on abduction** ([Collected Papers, Internet Archive](https://archive.org/search?query=peirce+collected+papers)) — the logic of hypothesis generation. Grounded scientific discovery. Relevant when an agent needs to propose explanations, not just verify them.
- **Ashby's Introduction to Cybernetics** (1956, [Internet Archive](https://archive.org/details/introductiontocy00ashb)) — the variety theorem, requisite variety, homeostasis. Grounded control theory. Directly applicable to system diagnosis: does the controller have enough variety to handle the disturbance?
- **Wittgenstein's Tractatus** (1921, [Project Gutenberg](https://www.gutenberg.org/ebooks/5740)) — formal language, truth tables, the picture theory. The propositional logic sections literally compile. The philosophy of language sections ground how specifications relate to the systems they describe.
- **Polya's How to Solve It** (1945) — heuristic problem-solving strategies. Every strategy is testable: does loading "have you seen this problem before?" improve an agent's problem-solving? Not yet public domain, but approaching.

### What doesn't qualify

Philosophy that's purely commentary, hermeneutics, or aesthetic appreciation. The test is functional: does it improve output on a task? Nietzsche is brilliant but doesn't make an agent better at diagnosing a data pipeline. Aristotle's logic does.

## What to exclude

- **Commentary without grounding.** Book reviews, literary criticism, pure hermeneutics. The exception: theory texts that ground a computable practice (see [theory texts](#second-vertical-theory-texts) above).
- **Fiction and poetry.** Aesthetic value, doesn't compile.
- **Encyclopedic summaries.** Too shallow — an agent needs the derivation, not the summary.
- **Content saturated in LLM training.** Shakespeare, the Constitution, the Bible. The LLM already has these. PageLeft's value is retrieval of what's *not* in the weights.
- **Scanned-but-not-OCR'd documents.** Can't chunk, can't embed, can't search.

## The test

> Would a vibelogger cite this page in a spec post and have the agent build from the citation?

If yes, index it. If the page is background reading but not buildable, skip it — the LLM already knows the background.

## Sources

- [Project Gutenberg](https://www.gutenberg.org/) — filter to nonfiction, philosophy, science, mathematics
- [Internet Archive](https://archive.org/) — public domain collection, filter to technical/reference
- [USPTO full text](https://www.uspto.gov/) — expired patents
- [RFC Editor](https://www.rfc-editor.org/) — full RFC corpus
- [NASA Technical Reports Server](https://ntrs.nasa.gov/)
- [NIST publications](https://www.nist.gov/publications)
