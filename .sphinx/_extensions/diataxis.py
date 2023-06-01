import re
import os

def setup_func(app, pagename, templatename, context, doctree):

    # Add a template function that inserts the Diataxis classes
    # into the toctree
    def add_diataxis_info(toctree):

        # Function to adds the Diataxis category for the linked file
        # for each match in the toctree
        def add_category(match):
            href = match.group(3)

            # Find the full path of the href (the href is relative
            # to the file being processed)
            if pagename == "index":
                path = os.path.normpath(href)
            elif href == "#":
                path = pagename
            else:
                path = os.path.normpath(os.path.join(pagename+"/"+href))

            # "." is actually the index page
            if path == ".":
                path = "index"

            # Find the Diataxis category, either from the file metadata
            # or from the page name
            category = ""
            if 'diataxisCategory' in app.env.metadata[path]:
                category = app.env.metadata[path]['diataxisCategory']
            elif path.startswith("howto"):
                category = "howto"
            elif path.startswith("reference"):
                category = "reference"
            elif path.startswith("explanation"):
                category = "explanation"
            elif path.startswith("tutorial"):
                category = "tutorial"

            # Always return what we matched, but maybe add to it
            start = "<li class=\"toctree"+match.group(1)+"><a "+match.group(2)+"href=\""+href+"\""

            if category:
                return start+" diataxis=\""+category+"\""
            else:
                return start

        # Add Diataxis categories to all toctree links
        return re.sub('<li class="toctree(.+?)><a (.+?) href="(.+?)"', add_category, toctree)

    context["add_diataxis_info"] = add_diataxis_info

def setup(app):
    app.connect("html-page-context", setup_func)

    return {"version": "0.1", "parallel_read_safe": True,
            "parallel_write_safe": True}
