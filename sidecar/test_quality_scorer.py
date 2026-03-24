"""Tests for quality_scorer feature extraction and rank aggregation."""

from quality_scorer import structural_score, domain_tier, rank_aggregate


def test_structural_score_code():
    text = "Here is some code:\n```python\nprint('hello')\n```\nEnd."
    score = structural_score(text)
    assert score >= 1, f"code fence should score >= 1, got {score}"


def test_structural_score_equations():
    text = r"The formula is $E = mc^2$ and also \(F = ma\)."
    score = structural_score(text)
    assert score >= 1, f"equations should score >= 1, got {score}"


def test_structural_score_citations():
    text = "See RFC 2616 for details. Also §501(c)(3) and DOI:10.1234/test."
    score = structural_score(text)
    assert score >= 1, f"citations should score >= 1, got {score}"


def test_structural_score_worked_examples():
    text = "Example: Consider a triangle.\nProof: By induction.\nSolution: x = 5."
    score = structural_score(text)
    assert score >= 1, f"worked examples should score >= 1, got {score}"


def test_structural_score_headings():
    text = "# Chapter 1\n\nSome text.\n\n## Section 1.1\n\nMore text.\n\n## Section 1.2\n\nEven more."
    score = structural_score(text)
    assert score >= 3, f"3 headings should contribute >= 3, got {score}"


def test_structural_score_empty():
    assert structural_score("") == 0
    assert structural_score("Just plain text with nothing special.") == 0


def test_structural_score_rich():
    text = """# Introduction
Some text.
## Method
```python
x = 1
```
The formula $E = mc^2$ applies.
Example: Consider RFC 2616.
- item 1
- item 2
- item 3
"""
    score = structural_score(text)
    assert score >= 5, f"rich content should score >= 5, got {score}"


def test_domain_tier_rfc():
    assert domain_tier("https://www.rfc-editor.org/rfc/rfc2616") == 1


def test_domain_tier_milewski():
    assert domain_tier("https://bartoszmilewski.com/2014/11/04/category") == 1


def test_domain_tier_libretexts():
    assert domain_tier("https://math.libretexts.org/some/chapter") == 2


def test_domain_tier_pressbooks():
    assert domain_tier("https://louis.pressbooks.pub/introstatistics/chapter/1") == 3


def test_domain_tier_unknown():
    assert domain_tier("https://random-site.com/page") == 4


def test_domain_tier_gutenberg():
    assert domain_tier("https://www.gutenberg.org/files/21076/21076-h/21076-h.htm") == 1


def test_domain_tier_uscode():
    assert domain_tier("https://uscode.house.gov/view.xhtml?req=granuleid") == 2


def test_rank_aggregate():
    pages = [
        {"id": 1, "structural": 10, "tier": 1},
        {"id": 2, "structural": 5, "tier": 2},
        {"id": 3, "structural": 0, "tier": 4},
        {"id": 4, "structural": 10, "tier": 3},
    ]
    scores = rank_aggregate(pages)
    # Page 1: best structure + best tier → highest quality
    # Page 3: worst structure + worst tier → lowest quality
    assert scores[1] > scores[2], "page 1 should beat page 2"
    assert scores[2] > scores[3], "page 2 should beat page 3"
    assert scores[1] > scores[4], "page 1 should beat page 4 (same structure, better tier)"
    # All scores between 0 and 1
    for pid, s in scores.items():
        assert 0.0 <= s <= 1.0, f"page {pid} score {s} out of range"
