from docutils import nodes
from docutils.parsers.rst import Directive

class YouTubeLink(Directive):

    required_arguments = 1
    optional_arguments = 0
    has_content = False

    def run(self):
        para = nodes.paragraph()
        para.set_class("youtube_link")

        para += nodes.reference(refuri=self.arguments[0], text="▶", internal=False)
        para += nodes.reference(refuri=self.arguments[0], text="Watch on YouTube", internal=False)

        return [para]


def setup(app):
    app.add_directive("youtube", YouTubeLink)

    return {
        'version': '0.1',
        'parallel_read_safe': True,
        'parallel_write_safe': True,
    }
