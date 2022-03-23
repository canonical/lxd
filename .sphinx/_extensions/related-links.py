import requests
import json

cache = {}

def setup_func(app, pagename, templatename, context, doctree):

    def discourse_links(IDlist):

        if context["discourse_prefix"] and IDlist:

            posts = IDlist.strip().replace(" ","").split(",")

            linklist = "<ul>";

            for post in posts:
                title = ""
                linkurl = context["discourse_prefix"]+post

                if post in cache:
                    title = cache[post]
                else:
                    try:
                        r = requests.get(linkurl+".json")
                        r.raise_for_status()
                        title = json.loads(r.text)["title"]
                        cache[post] = title
                    except requests.HTTPError as err:
                        print(err)

                if title:
                    linklist += '<li><a href="'+linkurl+'" target="_blank">'+title+'</a></li>'

            linklist += "</ul>"

            return linklist

        else:
            return ""

    context['discourse_links'] = discourse_links

def setup(app):
    app.connect("html-page-context", setup_func)

    return {
        'version': '0.1',
        'parallel_read_safe': True,
        'parallel_write_safe': True,
    }
