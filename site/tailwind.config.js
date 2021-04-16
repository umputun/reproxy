const colors = require('tailwindcss/colors')
const { spacing, width } = require('tailwindcss/defaultTheme')

module.exports = {
	mode: 'jit',
	purge: ['src/**/*.njk', 'src/**/*.js', '.eleventy.js'],
	darkMode: 'media',
	theme: {
		extend: {
			container: {
				center: true,
				sm: {
					with: '100%',
				},
			},
			colors: {
				orange: colors.orange,
			},
			typography: (theme) => ({
				DEFAULT: {
					css: {
						maxWidth: '100%',
						lineHeight: 1.5,
						paddingLeft: spacing[12],
						paddingRight: spacing[12],
						color: theme('colors.gray.700'),
						'h2,h3,h4': {
							paddingTop: spacing[24],
							marginTop: '-3rem',
						},
						'blockquote p:first-of-type::before': false,
						'blockquote p:last-of-type::after': false,
						'code::before': false,
						'code::after': false,
						img: {
							margin: spacing[2],
							display: 'initial',
						},
						code: {
							wordWrap: 'break-word',
							fontWeight: 'normal',
							backgroundColor: theme('colors.gray.100'),
							color: theme('colors.gray.700'),
							paddingTop: spacing[0.5],
							paddingBottom: spacing[0.5],
							paddingLeft: spacing[2],
							paddingRight: spacing[2],
							borderRadius: spacing[1],
						},
						hr: {
							borderTopWidth: '2px',
							borderColor: theme('colors.gray.700'),
						}
					},
				},
				lg: {
					css: {
						lineHeight: 1.5,
						'h2,h3,h4': {
							paddingTop: spacing[24],
							marginTop: '-3rem',
						},
					},
				},
				dark: {
					css: [
						{
							color: theme('colors.gray.400'),
							'[class~="lead"]': {
								color: theme('colors.gray.300'),
							},
							a: {
								color: theme('colors.gray.200'),
							},
							strong: {
								color: theme('colors.gray.200'),
							},
							'ol > li::before': {
								color: theme('colors.gray.400'),
							},
							'ul > li::before': {
								backgroundColor: theme('colors.gray.600'),
							},
							hr: {
								borderColor: theme('colors.gray.300'),
							},
							blockquote: {
								color: theme('colors.gray.300'),
								borderLeftColor: theme('colors.gray.600'),
							},
							h1: {
								color: theme('colors.gray.200'),
							},
							h2: {
								color: theme('colors.gray.200'),
							},
							h3: {
								color: theme('colors.gray.200'),
							},
							h4: {
								color: theme('colors.gray.200'),
							},
							'figure figcaption': {
								color: theme('colors.gray.400'),
							},
							code: {
								backgroundColor: theme('colors.gray.700'),
								color: theme('colors.gray.200'),
							},
							'a code': {
								color: theme('colors.gray.200'),
							},
							pre: {
								color: theme('colors.gray.300'),
								backgroundColor: theme('colors.gray.800'),
							},
							thead: {
								color: theme('colors.gray.200'),
								borderBottomColor: theme('colors.gray.400'),
							},
							'tbody tr': {
								borderBottomColor: theme('colors.gray.600'),
							},
							hr: {
								borderColor: theme('colors.gray.500'),
							}
						},
					],
				},
			}),
		},
	},
	variants: {
		extend: {
			typography: ['responsive', 'dark'],
		},
	},
	plugins: [require('@tailwindcss/typography')],
}
