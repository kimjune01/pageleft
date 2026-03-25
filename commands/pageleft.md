Search PageLeft for copyleft sources relevant to the current task.

Delegate to an Explore subagent. Only surface what's useful — don't pollute main context with raw JSON or noise.

## Usage

`/pageleft <query>`

## Instructions

Launch an Explore subagent:

1. Search: `curl -s "https://pageleft.cc/api/search?q=$(echo "$ARGUMENTS" | sed 's/ /+/g')&limit=10"`

2. Discard results scoring below 75% of the top result's `semantic_score`.

3. Filter false positives: does the snippet contain domain concepts for "$ARGUMENTS", or just keyword overlap? (e.g., "mechanism" matching legal text.) Discard the latter.

4. Do NOT fetch full pages. Use only the snippet/metadata from the search response.

5. For the top 1-3 relevant results, return a brief: title, URL, license, and a 1-2 sentence summary based on the search snippet. Nothing else.
