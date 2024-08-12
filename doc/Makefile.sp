# Minimal makefile for Sphinx documentation
#
# `Makefile.sp` is from the Sphinx starter pack and should not be
# modified.
# Add your customisation to `Makefile` instead.

# You can set these variables from the command line, and also
# from the environment for the first two.
SPHINXDIR     = .sphinx
SPHINXOPTS    ?= -c . -d $(SPHINXDIR)/.doctrees -j auto
SPHINXBUILD   ?= sphinx-build
SOURCEDIR     = .
BUILDDIR      = _build
VENVDIR       = $(SPHINXDIR)/venv
PA11Y         = $(SPHINXDIR)/node_modules/pa11y/bin/pa11y.js --config $(SPHINXDIR)/pa11y.json
VENV          = $(VENVDIR)/bin/activate
TARGET        = *
ALLFILES      =  *.rst **/*.rst
ADDPREREQS    ?=

.PHONY: sp-full-help sp-woke-install sp-pa11y-install sp-install sp-run sp-html \
        sp-epub sp-serve sp-clean sp-clean-doc sp-spelling sp-spellcheck sp-linkcheck sp-woke \
        sp-pa11y Makefile.sp sp-vale

sp-full-help: $(VENVDIR)
	@. $(VENV); $(SPHINXBUILD) -M help "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) $(O)
	@echo "\n\033[1;31mNOTE: This help texts shows unsupported targets!\033[0m"
	@echo "Run 'make help' to see supported targets."

# Shouldn't assume that venv is available on Ubuntu by default; discussion here:
# https://bugs.launchpad.net/ubuntu/+source/python3.4/+bug/1290847
$(SPHINXDIR)/requirements.txt:
	@python3 -c "import venv" || \
        (echo "You must install python3-venv before you can build the documentation."; exit 1)
	python3 -m venv $(VENVDIR)
	@if [ ! -z "$(ADDPREREQS)" ]; then \
          . $(VENV); pip install \
                         $(PIPOPTS) --require-virtualenv $(ADDPREREQS); \
        fi
	. $(VENV); python3 $(SPHINXDIR)/build_requirements.py

# If requirements are updated, venv should be rebuilt and timestamped.
$(VENVDIR): $(SPHINXDIR)/requirements.txt
	@echo "... setting up virtualenv"
	python3 -m venv $(VENVDIR)
	. $(VENV); pip install $(PIPOPTS) --require-virtualenv \
	    --upgrade -r $(SPHINXDIR)/requirements.txt \
            --log $(VENVDIR)/pip_install.log
	@test ! -f $(VENVDIR)/pip_list.txt || \
            mv $(VENVDIR)/pip_list.txt $(VENVDIR)/pip_list.txt.bak
	@. $(VENV); pip list --local --format=freeze > $(VENVDIR)/pip_list.txt
	@touch $(VENVDIR)

sp-woke-install:
	@type woke >/dev/null 2>&1 || \
            { echo "Installing \"woke\" snap... \n"; sudo snap install woke; }

sp-pa11y-install:
	@type $(PA11Y) >/dev/null 2>&1 || { \
			echo "Installing \"pa11y\" from npm... \n"; \
			mkdir -p $(SPHINXDIR)/node_modules/ ; \
			npm install --prefix $(SPHINXDIR) pa11y; \
		}

sp-install: $(VENVDIR)

sp-run: sp-install
	. $(VENV); sphinx-autobuild -b dirhtml "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS)

# Doesn't depend on $(BUILDDIR) to rebuild properly at every run.
sp-html: sp-install
	. $(VENV); $(SPHINXBUILD) -W --keep-going -b dirhtml "$(SOURCEDIR)" "$(BUILDDIR)" -w $(SPHINXDIR)/warnings.txt $(SPHINXOPTS)

sp-epub: sp-install
	. $(VENV); $(SPHINXBUILD) -b epub "$(SOURCEDIR)" "$(BUILDDIR)" -w $(SPHINXDIR)/warnings.txt $(SPHINXOPTS)

sp-serve: sp-html
	cd "$(BUILDDIR)"; python3 -m http.server --bind 127.0.0.1 8000

sp-clean: sp-clean-doc
	@test ! -e "$(VENVDIR)" -o -d "$(VENVDIR)" -a "$(abspath $(VENVDIR))" != "$(VENVDIR)"
	rm -rf $(VENVDIR)
	rm -f $(SPHINXDIR)/requirements.txt
	rm -rf $(SPHINXDIR)/node_modules/
	rm -rf $(SPHINXDIR)/styles
	rm -rf $(SPHINXDIR)/vale.ini

sp-clean-doc:
	git clean -fx "$(BUILDDIR)"
	rm -rf $(SPHINXDIR)/.doctrees

sp-spellcheck:
	. $(VENV) ; python3 -m pyspelling -c $(SPHINXDIR)/spellingcheck.yaml -j $(shell nproc)

sp-spelling: sp-html sp-spellcheck

sp-linkcheck: sp-install
	. $(VENV) ; $(SPHINXBUILD) -b linkcheck "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) || { grep --color -F "[broken]" "$(BUILDDIR)/output.txt"; exit 1; }
	exit 0

sp-woke: sp-woke-install
	woke $(ALLFILES) --exit-1-on-failure \
	    -c https://github.com/canonical/Inclusive-naming/raw/main/config.yml

sp-pa11y: sp-pa11y-install sp-html
	find $(BUILDDIR) -name *.html -print0 | xargs -n 1 -0 $(PA11Y)

sp-vale: sp-install
	@. $(VENV); test -d $(SPHINXDIR)/venv/lib/python*/site-packages/vale || pip install vale
	@. $(VENV); test -f $(SPHINXDIR)/vale.ini || python3 $(SPHINXDIR)/get_vale_conf.py
	@. $(VENV); find $(SPHINXDIR)/venv/lib/python*/site-packages/vale/vale_bin -size 195c -exec vale --config "$(SPHINXDIR)/vale.ini" $(TARGET) > /dev/null \;
	@echo ""
	@echo "Running Vale against $(TARGET). To change target set TARGET= with make command"
	@echo ""
	@. $(VENV); vale --config "$(SPHINXDIR)/vale.ini" --glob='*.{md,txt,rst}' $(TARGET)



# Catch-all target: route all unknown targets to Sphinx using the new
# "make mode" option.  $(O) is meant as a shortcut for $(SPHINXOPTS).
%: Makefile.sp
	. $(VENV); $(SPHINXBUILD) -M $@ "$(SOURCEDIR)" "$(BUILDDIR)" $(SPHINXOPTS) $(O)
