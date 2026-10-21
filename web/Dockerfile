# Hosbah web — production Next.js (real `next build` + `next start`, not `next dev`).
# Building inside the container sidesteps the host Turbopack out-of-root symlink
# failure. Do NOT set NODE_ENV=production before `npm ci` — it drops devDependencies
# (tailwind/postcss) and the build fails. `next build` is production regardless.
FROM node:25-alpine
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
# API_ORIGIN is where next's server-side rewrite sends /api.
ENV API_ORIGIN=http://api:8710
COPY package.json package-lock.json .npmrc ./
RUN npm ci
COPY . .
# NEXT_PUBLIC_* values are inlined at build time, so they are build args (docker build
# --build-arg …, or compose build.args). All are public: the MCP origin shown in token
# snippets, and Mahaam's Feedback SDK base URL and public key (empty = in-app report dialog).
ARG NEXT_PUBLIC_MCP_ORIGIN
ARG NEXT_PUBLIC_MAHAAM_URL
ARG NEXT_PUBLIC_MAHAAM_FEEDBACK_KEY
ENV NEXT_PUBLIC_MCP_ORIGIN=$NEXT_PUBLIC_MCP_ORIGIN \
    NEXT_PUBLIC_MAHAAM_URL=$NEXT_PUBLIC_MAHAAM_URL \
    NEXT_PUBLIC_MAHAAM_FEEDBACK_KEY=$NEXT_PUBLIC_MAHAAM_FEEDBACK_KEY
RUN npm run build
ENV NODE_ENV=production
EXPOSE 3710
CMD ["npm", "run", "start"]
