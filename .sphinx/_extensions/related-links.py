######################################################################
# This extension allows adding related links on a per-page basis
# in two ways (which can be combined):
#
# - Add links to Discourse topics by specifying the Discourse prefix
#   in the html_context variable in conf.py, for example:
#
#   html_context = {
#       "discourse_prefix": "https://discuss.linuxcontainers.org/t/"
#   }
#
#   Then add the topic IDs that you want to link to the metadata at
#   the top of the page using the tag "discourse".
#   For example (in MyST syntax):
#
#   ---
#   discourse: 12033,13128
#   ---
#
# - Add related URLs to the metadata at the top of the page using
#   the tag "relatedlinks". The link text is extracted automatically
#   or can be specified in Markdown syntax. Note that spaces are
#   ignored; if you need spaces in the title, replace them with &#32;.
#   For example (in MyST syntax):
#
#   ---
#   relatedlinks: https://www.example.com, [Link&#32;text](https://www.example.com)
#   ---
#
#   If Sphinx complains about the metadata value because it starts
#   with "[", enclose the full value in double quotes.
#
# For both ways, check for errors in the output. Invalid links are
# not added to the output.
######################################################################

import requests
import json
from bs4 import BeautifulSoup

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

    def related_links(linklist):

        if linklist:

            links = linklist.strip().replace(" ","").split(",")

            linklist = "<ul>";

            for link in links:
                title = ""

                if link in cache:
                    title = cache[link]
                elif link.startswith("[") and link.endswith(")"):
                    split = link.partition("](")
                    title = split[0][1:]
                    link = split[2][:-1]
                else:
                    try:
                        r = requests.get(link)
                        r.raise_for_status()
                        soup = BeautifulSoup(r.text, 'html.parser')
                        title = soup.title.get_text()
                        cache[link] = title
                    except requests.HTTPError as err:
                        print(err)

                if title:
                    linklist += '<li><a href="'+link+'" target="_blank">'+title+'</a></li>'

            linklist += "</ul>"

            return linklist

        else:
            return ""

    context['discourse_links'] = discourse_links
    context['related_links'] = related_links

def setup(app):
    app.connect("html-page-context", setup_func)

    return {
        'version': '0.1',
        'parallel_read_safe': True,
        'parallel_write_safe': True,
    }
