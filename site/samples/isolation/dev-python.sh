uv venv --python 3.11
source .venv/bin/activate
uv pip install -e .
helm dev -f helmstudio.yaml
