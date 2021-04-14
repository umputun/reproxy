const htmlmin = require('html-minifier')
const markdownIt = require('markdown-it')
const markdownItAnchor = require('markdown-it-anchor')
const toc = require('@thedigitalman/eleventy-plugin-toc-a11y')

function getVersion() {
	return `reproxy-${Date.now()}`
}

function transformHTML(content, outputPath) {
	if (!outputPath?.endsWith('.html')) {
		return content
	}

	return htmlmin.minify(content, {
		useShortDoctype: true,
		removeComments: true,
		collapseWhitespace: true,
	})
}

function transformMarkdown() {
	return markdownIt({
		html: true,
		breaks: true,
		linkify: true,
	}).use(markdownItAnchor, {
		permalink: true,
		permalinkClass: '',
		permalinkSymbol: '',
	})
}

module.exports = (config) => {
	config.setUseGitIgnore(false)
	config.addShortcode('version', getVersion)

	// Pluigns
	config.addPlugin(toc, {
		tags: ['h2', 'h3'],
		heading: false,
		listType: 'ul',
		wrapperClass: '',
		listClass: 'pl-4',
		listItemClass: 'mb-2',
		listItemAnchorClass: 'p-2',
	})

	// HTML transformations
	config.addTransform('htmlmin', transformHTML)

	// Markdown
	config.setLibrary('md', transformMarkdown())

	// Other files
	config.addPassthroughCopy('public/*')

	return {
		dir: {
			input: 'src',
			output: 'dist',
			data: 'data',
			layouts: 'layouts',
			includes: 'includes',
		},
	}
}
