"""prox worker for the Julia 2 decision models.

prox starts this once per model and talks JSON lines over stdin/stdout:

  <- {"ready": true, "model": "julia-2-flash", "device": "mps"}      after the model loads
  -> {"id": 1, "state": ..., "questions": {name: {type, instructions, criteria}}}
  <- {"id": 1, "answers": {...}, "input_tokens": 120}
  <- {"id": 1, "error": "..."}                                        when one request fails

Anything else the runtime prints goes to stderr, which prox keeps in its log.
"""
import json
import os
import sys

PROTOCOL = sys.stdout
sys.stdout = sys.stderr  # stray prints from the runtime must never corrupt the protocol pipe

WARMUP_QUESTIONS = {"w": {"type": "noul", "instructions": "Is this a warm-up?"}}


def send(msg):
    PROTOCOL.write(json.dumps(msg, ensure_ascii=False) + "\n")
    PROTOCOL.flush()


def pick_device(torch, wanted):
    if wanted and wanted != "auto":
        return wanted
    if torch.cuda.is_available():
        return "cuda"
    if torch.backends.mps.is_available():
        return "mps"
    return "cpu"


def load(model_dir, wanted_device):
    sys.path.insert(0, os.path.join(model_dir, "runtime"))
    import torch
    from julia2 import Runtime

    rt = Runtime(model_dir, modalities=("text",), device=pick_device(torch, wanted_device))
    # The first forward on a device compiles kernels (2.6 s on Apple GPUs); pay it before saying ready.
    rt.predict_questions("warm-up", WARMUP_QUESTIONS)
    return rt


def input_tokens(rt, state, questions):
    """Billable input the way System One counts it: the state once plus each question."""
    from julia2 import prompt as P

    state_text = state if isinstance(state, str) else json.dumps(state, ensure_ascii=False)
    total = len(rt.tok.encode(state_text))
    for q in questions.values():
        total += len(rt.tok.encode(rt._tail(q["type"], q.get("instructions", ""), P.options_for(q))))
    return total


def answer(rt, req):
    state, questions = req["state"], req["questions"]
    out = rt.predict_questions(state, questions)
    tokens = input_tokens(rt, state, questions)
    cache = getattr(rt.tok, "_cache", None)
    if cache is not None:
        cache.clear()  # the tokenizer memoises whole prompts; a long-lived worker would only grow
    return {"answers": out["answers"], "input_tokens": tokens}


def serve(rt):
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        req_id = None
        try:
            req = json.loads(line)
            req_id = req.get("id")
            send({"id": req_id, **answer(rt, req)})
        except Exception as exc:  # one bad request must not take the model down
            send({"id": req_id, "error": f"{type(exc).__name__}: {exc}"})


def main():
    model_dir, device = sys.argv[1], (sys.argv[2] if len(sys.argv) > 2 else "auto")
    try:
        rt = load(model_dir, device)
    except Exception as exc:
        send({"error": f"could not load the model: {type(exc).__name__}: {exc}"})
        return 1
    send({"ready": True, "model": rt.cfg.get("name", ""), "device": str(rt.device)})
    serve(rt)
    return 0


if __name__ == "__main__":
    sys.exit(main())
