FROM node:14-alpine

WORKDIR build

COPY . /build
RUN yarn --frozen-lockfile && \
	yarn build && \
	ls -la /public

CMD ["sleep", "100"]

